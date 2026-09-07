// Package sandbox constrains what the DevOps agent can touch. Every filesystem
// tool resolves paths through a jail rooted at a single workspace directory,
// and every shell command must match an allowlist of bare command names. A
// dry-run mode turns mutating operations into descriptions of what *would*
// happen, so the agent can be evaluated safely before it is trusted to act.
//
// Path containment is enforced both lexically and against symlinks: a symlink
// inside the workspace that points outside it cannot be used to escape.
package sandbox

import (
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
	Root      string        // absolute, symlink-resolved filesystem jail
	Allow     []string      // allowlisted bare command names for Run
	Timeout   time.Duration // per-command timeout
	MaxOutput int           // cap on captured bytes (output and file reads)
	DryRun    bool          // if true, mutating ops are described, not performed
}

// New creates a sandbox rooted at dir, which must exist and be a directory.
// The root is resolved through symlinks so containment checks are exact.
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
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return &Sandbox{
		Root:      abs,
		Timeout:   10 * time.Second,
		MaxOutput: 64 * 1024,
	}, nil
}

// Resolve turns a user/agent-supplied path into an absolute path guaranteed to
// live within the sandbox root — even after following symlinks — or returns
// ErrEscape.
func (s *Sandbox) Resolve(p string) (string, error) {
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(s.Root, p)
	}
	abs = filepath.Clean(abs)

	// Lexical containment first.
	if !s.within(abs) {
		return "", ErrEscape
	}
	// Then follow symlinks on the existing prefix and re-check, so a symlink
	// inside the workspace cannot point outside it.
	if !s.within(s.evalExisting(abs)) {
		return "", ErrEscape
	}
	return abs, nil
}

// within reports whether path is inside (or equal to) the root, lexically.
func (s *Sandbox) within(path string) bool {
	rel, err := filepath.Rel(s.Root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// evalExisting resolves symlinks on the longest existing ancestor of abs and
// re-appends the not-yet-existing tail (for paths being created).
func (s *Sandbox) evalExisting(abs string) string {
	cur := abs
	var tail []string
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				resolved = cur
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// Allowed reports whether a command is permitted to run. The name must be a
// bare command (no path separators): the allowlist restricts *which binary*,
// not just its filename, and resolution goes through PATH — so a workspace file
// named like an allowlisted command cannot be executed.
func (s *Sandbox) Allowed(name string) bool {
	if name == "" || strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return false
	}
	for _, a := range s.Allow {
		if a == name {
			return true
		}
	}
	return false
}

// Run executes an allowlisted command inside the sandbox root with a timeout,
// capturing combined stdout+stderr bounded to MaxOutput bytes *during*
// execution (so a chatty process cannot exhaust memory). In DryRun mode it
// returns a description instead of executing.
func (s *Sandbox) Run(ctx context.Context, name string, args []string) (string, error) {
	if !s.Allowed(name) {
		return "", fmt.Errorf("command %q is not an allowlisted bare command name %v", name, s.Allow)
	}
	if s.DryRun {
		return fmt.Sprintf("[dry-run] would execute: %s %s", name, strings.Join(args, " ")), nil
	}

	cctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Dir = s.Root

	cw := &capWriter{limit: s.MaxOutput}
	cmd.Stdout = cw
	cmd.Stderr = cw
	runErr := cmd.Run()

	out := cw.String()
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

// capWriter is an io.Writer that keeps at most `limit` bytes in memory and
// discards the rest, so command output cannot exhaust memory. It always reports
// a full write so the child process is never blocked or errored.
type capWriter struct {
	buf       []byte
	limit     int
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	limit := w.limit
	if limit <= 0 {
		limit = 1 << 20
	}
	if len(w.buf) < limit {
		room := limit - len(w.buf)
		if room >= len(p) {
			w.buf = append(w.buf, p...)
		} else {
			w.buf = append(w.buf, p[:room]...)
			w.truncated = true
		}
	} else {
		w.truncated = true
	}
	return len(p), nil
}

func (w *capWriter) String() string {
	if w.truncated {
		return string(w.buf) + "\n...[truncated]"
	}
	return string(w.buf)
}
