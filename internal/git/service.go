package git

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Service represents a git service type.
type Service string

const (
	UploadPackService  Service = "git-upload-pack"
	ReceivePackService Service = "git-receive-pack"
)

func (s Service) String() string {
	return string(s)
}

// ServiceCommand is a git service command that can be executed against a
// bare repository. It wraps exec.Cmd with the right arguments for git
// smart HTTP and SSH protocol operations.
type ServiceCommand struct {
	Dir    string
	Args   []string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes the git service command.
func (s Service) Run(ctx context.Context, cmd ServiceCommand) error {
	args := []string{s.subcommand()}
	args = append(args, cmd.Args...)
	args = append(args, cmd.Dir)

	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = cmd.Dir
	c.Env = cmd.Env
	c.Stdin = cmd.Stdin
	c.Stdout = cmd.Stdout
	c.Stderr = cmd.Stderr

	if err := c.Run(); err != nil {
		return fmt.Errorf("git %s: %w", s, err)
	}
	return nil
}

// subcommand returns the git subcommand name (without "git-" prefix).
func (s Service) subcommand() string {
	switch s {
	case UploadPackService:
		return "upload-pack"
	case ReceivePackService:
		return "receive-pack"
	default:
		return string(s)
	}
}

// WritePktline writes a pktline formatted string to w.
// This is used for the smart HTTP info/refs response.
func WritePktline(w io.Writer, s string) error {
	msg := fmt.Sprintf("%04x%s\n", len(s)+5, s)
	_, err := io.WriteString(w, msg)
	if err != nil {
		return fmt.Errorf("write pktline: %w", err)
	}
	// Flush pktline
	_, err = io.WriteString(w, "0000")
	return err
}
