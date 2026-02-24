package git

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// CommitInfo holds summary information about a commit.
type CommitInfo struct {
	Hash       string
	ShortHash  string
	Message    string
	Author     string
	AuthorEmail string
	Date       time.Time
	Signature  string // SSH signature if present
}

// FileEntry represents a file or directory in a tree listing.
type FileEntry struct {
	Name  string
	IsDir bool
	Size  int64
}

// RefInfo holds information about a branch or tag.
type RefInfo struct {
	Name      string
	Hash      string
	ShortHash string
	IsTag     bool
}

// DiffStat holds per-file diff statistics.
type DiffStat struct {
	Name      string
	Additions int
	Deletions int
}

// DiffResult holds the full diff output for a commit.
type DiffResult struct {
	Stats []DiffStat
	Patch string
}

// OpenRepo opens a bare git repository at the given path.
func OpenRepo(reposPath, name string) (*git.Repository, error) {
	path := filepath.Join(reposPath, name+".git")
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("open repo %s: %w", name, err)
	}
	return repo, nil
}

// ListRefs returns all branches and tags for a repository.
func ListRefs(repo *git.Repository) ([]RefInfo, error) {
	var refs []RefInfo

	// Branches
	branches, err := repo.Branches()
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	err = branches.ForEach(func(ref *plumbing.Reference) error {
		refs = append(refs, RefInfo{
			Name:      ref.Name().Short(),
			Hash:      ref.Hash().String(),
			ShortHash: ref.Hash().String()[:7],
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Tags
	tags, err := repo.Tags()
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	err = tags.ForEach(func(ref *plumbing.Reference) error {
		refs = append(refs, RefInfo{
			Name:      ref.Name().Short(),
			Hash:      ref.Hash().String(),
			ShortHash: ref.Hash().String()[:7],
			IsTag:     true,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return refs, nil
}

// resolveRef resolves a ref string to a commit hash. The ref can be a branch
// name, tag name, short hash, or full hash.
func resolveRef(repo *git.Repository, ref string) (*plumbing.Hash, error) {
	// Try as a branch
	branchRef, err := repo.Reference(plumbing.NewBranchReferenceName(ref), true)
	if err == nil {
		h := branchRef.Hash()
		return &h, nil
	}

	// Try as a tag
	tagRef, err := repo.Reference(plumbing.NewTagReferenceName(ref), true)
	if err == nil {
		h := tagRef.Hash()
		return &h, nil
	}

	// Try as HEAD
	if ref == "HEAD" || ref == "" {
		headRef, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("resolve HEAD: %w", err)
		}
		h := headRef.Hash()
		return &h, nil
	}

	// Try as a hash (full or short)
	if len(ref) >= 4 {
		h := plumbing.NewHash(ref)
		if h.IsZero() {
			// Might be a short hash — iterate commits to find it
			// For now, only support full hashes
			return nil, fmt.Errorf("unknown ref: %s", ref)
		}
		return &h, nil
	}

	return nil, fmt.Errorf("unknown ref: %s", ref)
}

// Log returns paginated commit history for a given ref.
func Log(repo *git.Repository, ref string, page, perPage int) ([]CommitInfo, bool, error) {
	hash, err := resolveRef(repo, ref)
	if err != nil {
		return nil, false, err
	}

	iter, err := repo.Log(&git.LogOptions{
		From:  *hash,
		Order: git.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, false, fmt.Errorf("log: %w", err)
	}
	defer iter.Close()

	// Skip to page
	skip := page * perPage
	for range skip {
		if _, err := iter.Next(); err != nil {
			return nil, false, nil // No more commits
		}
	}

	var commits []CommitInfo
	for i := 0; i < perPage+1; i++ { // fetch one extra to check if there's a next page
		c, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("iterate log: %w", err)
		}

		if i < perPage {
			commits = append(commits, commitToInfo(c))
		}
	}

	hasMore := len(commits) == perPage // we fetched perPage+1 items
	return commits, hasMore, nil
}

// Tree returns the directory listing at a path for a given ref.
func Tree(repo *git.Repository, ref, path string) ([]FileEntry, error) {
	hash, err := resolveRef(repo, ref)
	if err != nil {
		return nil, err
	}

	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("get commit: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("get tree: %w", err)
	}

	// Navigate to subdirectory if path is not root
	if path != "" && path != "." && path != "/" {
		tree, err = tree.Tree(path)
		if err != nil {
			return nil, fmt.Errorf("get subtree %s: %w", path, err)
		}
	}

	var entries []FileEntry
	for _, entry := range tree.Entries {
		fe := FileEntry{
			Name:  entry.Name,
			IsDir: entry.Mode.IsFile() == false,
		}

		// Get file size for files
		if !fe.IsDir {
			f, err := tree.TreeEntryFile(&entry)
			if err == nil {
				fe.Size = f.Size
			}
		}

		entries = append(entries, fe)
	}

	// Sort: directories first, then alphabetical
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	return entries, nil
}

// Blob returns the content of a file at a given ref and path.
func Blob(repo *git.Repository, ref, path string) (string, int64, error) {
	hash, err := resolveRef(repo, ref)
	if err != nil {
		return "", 0, err
	}

	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return "", 0, fmt.Errorf("get commit: %w", err)
	}

	file, err := commit.File(path)
	if err != nil {
		return "", 0, fmt.Errorf("get file %s: %w", path, err)
	}

	content, err := file.Contents()
	if err != nil {
		return "", 0, fmt.Errorf("read file %s: %w", path, err)
	}

	return content, file.Size, nil
}

// Diff returns the unified diff for a commit.
func Diff(repo *git.Repository, commitHash string) (*DiffResult, *CommitInfo, error) {
	h := plumbing.NewHash(commitHash)
	commit, err := repo.CommitObject(h)
	if err != nil {
		return nil, nil, fmt.Errorf("get commit: %w", err)
	}

	info := commitToInfo(commit)

	commitTree, err := commit.Tree()
	if err != nil {
		return nil, nil, fmt.Errorf("get commit tree: %w", err)
	}

	var parentTree *object.Tree
	if commit.NumParents() > 0 {
		parent, err := commit.Parent(0)
		if err != nil {
			return nil, nil, fmt.Errorf("get parent: %w", err)
		}
		parentTree, err = parent.Tree()
		if err != nil {
			return nil, nil, fmt.Errorf("get parent tree: %w", err)
		}
	}

	changes, err := object.DiffTree(parentTree, commitTree)
	if err != nil {
		return nil, nil, fmt.Errorf("diff tree: %w", err)
	}

	patch, err := changes.Patch()
	if err != nil {
		return nil, nil, fmt.Errorf("generate patch: %w", err)
	}

	result := &DiffResult{}

	// Build stats from patch file stats
	for _, stat := range patch.Stats() {
		result.Stats = append(result.Stats, DiffStat{
			Name:      stat.Name,
			Additions: stat.Addition,
			Deletions: stat.Deletion,
		})
	}

	// Get full patch text
	var buf bytes.Buffer
	if err := patch.Encode(&buf); err != nil {
		return nil, nil, fmt.Errorf("encode patch: %w", err)
	}
	result.Patch = buf.String()

	return result, &info, nil
}

// Readme tries to find and return the content of a README file at the repo root.
func Readme(repo *git.Repository, ref string) (string, string, error) {
	hash, err := resolveRef(repo, ref)
	if err != nil {
		return "", "", err
	}

	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return "", "", fmt.Errorf("get commit: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return "", "", fmt.Errorf("get tree: %w", err)
	}

	// Try common README filenames
	names := []string{
		"README.md", "readme.md", "Readme.md",
		"README", "readme",
		"README.txt", "readme.txt",
		"README.rst", "readme.rst",
	}

	for _, name := range names {
		file, err := tree.File(name)
		if err != nil {
			continue
		}
		content, err := file.Contents()
		if err != nil {
			continue
		}
		return content, name, nil
	}

	return "", "", nil // No readme found
}

// DefaultBranch returns the default branch of a repository by inspecting HEAD.
func DefaultBranch(repo *git.Repository) string {
	head, err := repo.Head()
	if err != nil {
		return "main"
	}
	return head.Name().Short()
}

func commitToInfo(c *object.Commit) CommitInfo {
	info := CommitInfo{
		Hash:        c.Hash.String(),
		ShortHash:   c.Hash.String()[:7],
		Message:     strings.TrimSpace(c.Message),
		Author:      c.Author.Name,
		AuthorEmail: c.Author.Email,
		Date:        c.Author.When,
	}
	if c.PGPSignature != "" {
		info.Signature = c.PGPSignature
	}
	return info
}
