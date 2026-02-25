package http

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gitpkg "github.com/wbrijesh/origin/internal/git"
)

// baseData returns common template data for every page.
func (s *Server) baseData(r *http.Request) map[string]any {
	return map[string]any{
		"ServerName": s.cfg.Name,
		"LoggedIn":   s.isLoggedIn(r),
	}
}

// isLoggedIn checks if the current request has a valid session.
func (s *Server) isLoggedIn(r *http.Request) bool {
	cookie, err := r.Cookie("session")
	if err != nil {
		return false
	}
	var expiresAt time.Time
	err = s.db.Get(&expiresAt, "SELECT expires_at FROM sessions WHERE id = ?", cookie.Value)
	if err != nil {
		return false
	}
	return time.Now().Before(expiresAt)
}

type repoRow struct {
	Name        string    `db:"name"`
	Description string    `db:"description"`
	IsPrivate   bool      `db:"is_private"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	// Redirect to setup wizard on first run
	if s.needsSetup() {
		http.Redirect(w, r, "/-/setup", http.StatusSeeOther)
		return
	}

	data := s.baseData(r)
	data["Title"] = ""

	loggedIn := data["LoggedIn"].(bool)

	var repos []repoRow
	var err error
	if loggedIn {
		err = s.db.Select(&repos, "SELECT name, description, is_private, updated_at FROM repositories ORDER BY updated_at DESC")
	} else {
		err = s.db.Select(&repos, "SELECT name, description, is_private, updated_at FROM repositories WHERE is_private = 0 ORDER BY updated_at DESC")
	}
	if err != nil {
		slog.Error("query repos", "error", err)
	}

	data["Repos"] = repos
	s.render.render(w, "home", data)
}

func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	data := s.baseData(r)

	// Check repo exists and is accessible
	var repo repoRow
	err := s.db.Get(&repo, "SELECT name, description, is_private, updated_at FROM repositories WHERE name = ?", repoName)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}
	if repo.IsPrivate && !data["LoggedIn"].(bool) {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	data["Title"] = repoName
	data["RepoName"] = repoName
	data["Description"] = repo.Description
	data["IsPrivate"] = repo.IsPrivate
	data["ActiveTab"] = "files"
	data["CloneSSH"] = fmt.Sprintf("%s/%s", s.cfg.SSHCloneBase(), repoName)
	data["CloneHTTP"] = fmt.Sprintf("%s/%s", s.cfg.HTTP.PublicURL, repoName)

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		data["IsEmpty"] = true
		data["DefaultBranch"] = "main"
		s.render.render(w, "repo", data)
		return
	}

	defaultBranch := gitpkg.DefaultBranch(gitRepo)
	data["DefaultBranch"] = defaultBranch
	data["IsEmpty"] = false

	// File tree at root
	entries, err := gitpkg.Tree(gitRepo, defaultBranch, "")
	if err != nil {
		data["IsEmpty"] = true
		s.render.render(w, "repo", data)
		return
	}
	data["Entries"] = entries

	// README
	readmeContent, readmeFile, _ := gitpkg.Readme(gitRepo, defaultBranch)
	if readmeContent != "" {
		if strings.HasSuffix(strings.ToLower(readmeFile), ".md") {
			data["Readme"] = s.render.renderMarkdown(readmeContent)
		} else {
			data["Readme"] = template.HTML("<pre>" + template.HTMLEscapeString(readmeContent) + "</pre>") //nolint:gosec
		}
		data["ReadmeFile"] = readmeFile
	}

	s.render.render(w, "repo", data)
}

// Breadcrumb represents a path segment for navigation.
type Breadcrumb struct {
	Name   string
	Path   string
	IsLast bool
}

func buildBreadcrumbs(path string) []Breadcrumb {
	if path == "" || path == "." {
		return nil
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	crumbs := make([]Breadcrumb, len(parts))
	for i, part := range parts {
		crumbs[i] = Breadcrumb{
			Name:   part,
			Path:   strings.Join(parts[:i+1], "/"),
			IsLast: i == len(parts)-1,
		}
	}
	return crumbs
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	ref := r.PathValue("ref")
	path := r.PathValue("path")

	if !s.canAccessRepo(repoName, r) {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	data := s.baseData(r)
	data["Title"] = fmt.Sprintf("%s — %s", repoName, path)
	data["RepoName"] = repoName
	data["Ref"] = ref
	data["ActiveTab"] = "files"
	data["Breadcrumbs"] = buildBreadcrumbs(path)

	// Set current path for building child links
	currentPath := path
	if currentPath != "" && !strings.HasSuffix(currentPath, "/") {
		currentPath += "/"
	}
	data["CurrentPath"] = currentPath

	// Parent path for ".." navigation
	if path != "" && path != "." {
		parent := filepath.Dir(path)
		if parent == "." {
			parent = ""
		}
		data["ParentPath"] = parent
	}

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to open repository")
		return
	}

	entries, err := gitpkg.Tree(gitRepo, ref, path)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Path not found")
		return
	}
	data["Entries"] = entries

	s.loadRepoMeta(data, repoName)
	s.render.render(w, "tree", data)
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	ref := r.PathValue("ref")
	path := r.PathValue("path")

	if !s.canAccessRepo(repoName, r) {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	data := s.baseData(r)
	data["Title"] = fmt.Sprintf("%s — %s", repoName, filepath.Base(path))
	data["RepoName"] = repoName
	data["Ref"] = ref
	data["FileName"] = filepath.Base(path)
	data["Breadcrumbs"] = buildBreadcrumbs(path)

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to open repository")
		return
	}

	content, size, err := gitpkg.Blob(gitRepo, ref, path)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "File not found")
		return
	}

	data["FileSize"] = formatSize(size)
	data["HighlightedContent"] = highlightCode(content, filepath.Base(path))

	s.loadRepoMeta(data, repoName)
	s.render.render(w, "file", data)
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	ref := r.PathValue("ref")

	if !s.canAccessRepo(repoName, r) {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	page := 0
	if p := r.URL.Query().Get("page"); p != "" {
		page, _ = strconv.Atoi(p)
		if page < 0 {
			page = 0
		}
	}

	data := s.baseData(r)
	data["Title"] = fmt.Sprintf("%s — commits", repoName)
	data["RepoName"] = repoName
	data["Ref"] = ref
	data["ActiveTab"] = "commits"
	data["Page"] = page
	data["HasPrev"] = page > 0

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to open repository")
		return
	}

	commits, hasMore, err := gitpkg.Log(gitRepo, ref, page, 30)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Ref not found")
		return
	}

	data["Commits"] = commits
	data["HasNext"] = hasMore

	s.loadRepoMeta(data, repoName)
	s.render.render(w, "log", data)
}

// DiffLine represents a single line in a diff, with type info for coloring.
type DiffLine struct {
	Text   string
	IsAdd  bool
	IsDel  bool
	IsHunk bool
}

func parseDiffLines(patch string) []DiffLine {
	var lines []DiffLine
	for _, line := range strings.Split(patch, "\n") {
		dl := DiffLine{Text: line}
		switch {
		case strings.HasPrefix(line, "+"):
			dl.IsAdd = true
		case strings.HasPrefix(line, "-"):
			dl.IsDel = true
		case strings.HasPrefix(line, "@@"):
			dl.IsHunk = true
		}
		lines = append(lines, dl)
	}
	return lines
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	hash := r.PathValue("hash")

	if !s.canAccessRepo(repoName, r) {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	data := s.baseData(r)
	data["Title"] = fmt.Sprintf("%s — %s", repoName, hash[:7])
	data["RepoName"] = repoName

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to open repository")
		return
	}

	diff, commit, err := gitpkg.Diff(gitRepo, hash)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Commit not found")
		return
	}

	data["Commit"] = commit
	data["Diff"] = diff
	data["DiffLines"] = parseDiffLines(diff.Patch)

	// Extract full message (lines after first)
	msg := commit.Message
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		rest := strings.TrimSpace(msg[i+1:])
		if rest != "" {
			data["FullMessage"] = rest
		}
	}

	s.loadRepoMeta(data, repoName)
	s.render.render(w, "commit", data)
}

func (s *Server) handleRefs(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))

	if !s.canAccessRepo(repoName, r) {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	data := s.baseData(r)
	data["Title"] = fmt.Sprintf("%s — refs", repoName)
	data["RepoName"] = repoName
	data["ActiveTab"] = "refs"

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to open repository")
		return
	}

	data["DefaultBranch"] = gitpkg.DefaultBranch(gitRepo)

	refs, err := gitpkg.ListRefs(gitRepo)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to list refs")
		return
	}

	var branches, tags []gitpkg.RefInfo
	for _, ref := range refs {
		if ref.IsTag {
			tags = append(tags, ref)
		} else {
			branches = append(branches, ref)
		}
	}

	data["Branches"] = branches
	data["Tags"] = tags

	s.loadRepoMeta(data, repoName)
	s.render.render(w, "refs", data)
}

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	ref := r.PathValue("ref")
	ref = strings.TrimSuffix(ref, ".tar.gz")

	if !s.canAccessRepo(repoName, r) {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to open repository")
		return
	}

	// Get all files recursively
	files, err := gitpkg.Archive(gitRepo, ref)
	if err != nil {
		s.renderError(w, r, http.StatusNotFound, "Ref not found")
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-%s.tar.gz", repoName, ref))

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	prefix := repoName + "-" + ref + "/"
	for _, f := range files {
		tw.WriteHeader(&tar.Header{ //nolint:errcheck
			Name:    prefix + f.Path,
			Size:    int64(len(f.Content)),
			Mode:    0o644,
			ModTime: time.Now(),
		})
		io.WriteString(tw, f.Content) //nolint:errcheck
	}
}

// canAccessRepo checks if a repo is accessible for the current request.
func (s *Server) canAccessRepo(name string, r *http.Request) bool {
	var isPrivate bool
	err := s.db.Get(&isPrivate, "SELECT is_private FROM repositories WHERE name = ?", name)
	if err != nil {
		return false
	}
	if isPrivate && !s.isLoggedIn(r) {
		return false
	}
	return true
}

// loadRepoMeta loads common repo metadata into template data.
func (s *Server) loadRepoMeta(data map[string]any, repoName string) {
	var repo repoRow
	if err := s.db.Get(&repo, "SELECT name, description, is_private, updated_at FROM repositories WHERE name = ?", repoName); err == nil {
		data["Description"] = repo.Description
		data["IsPrivate"] = repo.IsPrivate
	}
	// Ensure DefaultBranch is set for repo-tabs partial.
	if _, ok := data["DefaultBranch"]; !ok {
		gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
		if err == nil {
			data["DefaultBranch"] = gitpkg.DefaultBranch(gitRepo)
		} else {
			data["DefaultBranch"] = "main"
		}
	}
}

// renderError renders an error page.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, code int, message string) {
	w.WriteHeader(code)
	data := s.baseData(r)
	data["Title"] = fmt.Sprintf("%d", code)
	data["Code"] = code
	data["Message"] = message
	s.render.render(w, "error", data)
}
