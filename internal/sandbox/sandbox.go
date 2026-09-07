// Package sandbox constrains what the DevOps agent can touch. Every filesystem
// tool resolves paths through a jail rooted at a single workspace directory,
// and every shell command must match an allowlist. A dry-run mode turns
// mutating operations into descriptions of what *would* happen, so the agent
// can be evaluated safely before it is trusted to act.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrEscape is returned when a path would resolve outside the sandbox root.
var ErrEscape = errors.New("path escapes sandbox root")

// Sandbox is a workspace jail plus a command allowlist.
type Sandbox struct {
	Root      string        // absolute filesystem jail
	Allow     []string      // allowlisted command basenames for Run
	Timeout   time.Duration // per-command timeout
	MaxOutput int           // cap on captured bytes (output and file reads)
	DryRun    bool          // if true, mutating ops are described, not performed
}

// New creates a sandbox rooted at dir, which must exist and be a directory.
func New(dir string) (*Sandbox, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sandbox root %q is not a directory", abs)
	}
	return &Sandbox{
		Root:      abs,
		Timeout:   10 * time.Second,
		MaxOutput: 64 * 1024,
	}, nil
}

// Resolve turns a user/agent-supplied path into an absolute path guaranteed to
// live within the sandbox root, or returns ErrEscape.
func (s *Sandbox) Resolve(p string) (string, error) {
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(s.Root, p)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(s.Root, abs)
	if err != nil {
		return "", ErrEscape
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrEscape
	}
	return abs, nil
}

// Allowed reports whether a command is permitted to run.
func (s *Sandbox) Allowed(name string) bool {
	base := filepath.Base(name)
	for _, a := range s.Allow {
		if a == base || a == name {
			return true
		}
	}
	return false
}

// Run executes an allowlisted command inside the sandbox root with a timeout,
// capturing combined stdout+stderr (truncated to MaxOutput). In DryRun mode it
// returns a description instead of executing.
func (s *Sandbox) Run(ctx context.Context, name string, args []string) (string, error) {
	if !s.Allowed(name) {
		return "", fmt.Errorf("command %q is not in the allowlist %v", name, s.Allow)
	}
	if s.DryRun {
		return fmt.Sprintf("[dry-run] would execute: %s %s", name, strings.Join(args, " ")), nil
	}

	cctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Dir = s.Root

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()

	out := buf.String()
	if s.MaxOutput > 0 && len(out) > s.MaxOutput {
		out = out[:s.MaxOutput] + "\n...[truncated]"
	}
	if cctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("command timed out after %s", s.Timeout)
	}
	if runErr != nil {
		return out, fmt.Errorf("command exited with error: %w", runErr)
	}
	return out, nil
}

// Cap truncates b to MaxOutput bytes for consistent tool output sizing.
func (s *Sandbox) Cap(b []byte) []byte {
	if s.MaxOutput > 0 && len(b) > s.MaxOutput {
		return append(b[:s.MaxOutput:s.MaxOutput], []byte("\n...[truncated]")...)
	}
	return b
}
