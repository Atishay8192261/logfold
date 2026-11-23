// Package devops implements the tools the flagship LogFold agent uses to
// diagnose and fix a service: reading files, listing directories, grepping,
// tailing logs, probing ports and HTTP endpoints, running allowlisted
// commands, and applying file edits. Every tool goes through the sandbox, so
// the agent can only touch its workspace and approved commands.
package devops

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Atishay8192261/logfold/internal/sandbox"
	"github.com/Atishay8192261/logfold/internal/tool"
)

// Register installs the full DevOps toolset into reg, backed by sb.
func Register(reg *tool.Registry, sb *sandbox.Sandbox) {
	reg.Register(&readFile{sb})
	reg.Register(&listDir{sb})
	reg.Register(&grep{sb})
	reg.Register(&tailLog{sb})
	reg.Register(&writeFile{sb})
	reg.Register(&runCmd{sb})
	reg.Register(&httpProbe{sb})
	reg.Register(&checkPort{sb})
}

// --- read_file ---

type readFile struct{ sb *sandbox.Sandbox }

func (t *readFile) Name() string { return "read_file" }
func (t *readFile) Description() string {
	return "Read a text file in the workspace and return its contents."
}
func (t *readFile) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "path", Type: tool.TypeString, Required: true, Description: "File path relative to the workspace root."},
	}}
}
func (t *readFile) Invoke(_ context.Context, a tool.Args) (string, error) {
	abs, err := t.sb.Resolve(a.String("path"))
	if err != nil {
		return "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	lim := t.sb.MaxOutput
	if lim <= 0 {
		lim = 1 << 20
	}
	// Read at most one byte past the cap so we never pull a huge file into RAM;
	// Cap then truncates to the limit with a marker.
	b, err := io.ReadAll(io.LimitReader(f, int64(lim)+1))
	if err != nil {
		return "", err
	}
	return string(t.sb.Cap(b)), nil
}

// --- list_dir ---

type listDir struct{ sb *sandbox.Sandbox }

func (t *listDir) Name() string        { return "list_dir" }
func (t *listDir) Description() string { return "List the entries of a directory in the workspace." }
func (t *listDir) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "path", Type: tool.TypeString, Default: ".", Description: "Directory path relative to the workspace root."},
	}}
}
func (t *listDir) Invoke(_ context.Context, a tool.Args) (string, error) {
	p := a.String("path")
	if p == "" {
		p = "."
	}
	abs, err := t.sb.Resolve(p)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		info, ierr := e.Info()
		size := int64(0)
		if ierr == nil {
			size = info.Size()
		}
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		fmt.Fprintf(&b, "%-4s  %8d  %s\n", kind, size, e.Name())
	}
	if b.Len() == 0 {
		return "(empty directory)", nil
	}
	return b.String(), nil
}

// --- grep ---

type grep struct{ sb *sandbox.Sandbox }

func (t *grep) Name() string { return "grep" }
func (t *grep) Description() string {
	return "Search files under a path for a regular expression. Returns path:line matches."
}
func (t *grep) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "pattern", Type: tool.TypeString, Required: true, Description: "RE2 regular expression to search for."},
		{Name: "path", Type: tool.TypeString, Default: ".", Description: "File or directory to search, relative to the workspace root."},
	}}
}
func (t *grep) Invoke(_ context.Context, a tool.Args) (string, error) {
	re, err := regexp.Compile(a.String("pattern"))
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %w", err)
	}
	p := a.String("path")
	if p == "" {
		p = "."
	}
	root, err := t.sb.Resolve(p)
	if err != nil {
		return "", err
	}

	const maxMatches = 200
	var matches []string
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(t.sb.Root, path)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		line := 0
		for sc.Scan() {
			line++
			if re.MatchString(sc.Text()) {
				text := strings.TrimSpace(sc.Text())
				if len(text) > 500 {
					text = text[:500] + "…"
				}
				matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, line, text))
				if len(matches) >= maxMatches {
					return fs.SkipAll
				}
			}
		}
		return nil
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if err := filepath.WalkDir(root, walk); err != nil {
			return "", err
		}
	} else {
		_ = walk(root, fs.FileInfoToDirEntry(info), nil)
	}

	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return string(t.sb.Cap([]byte(strings.Join(matches, "\n")))), nil
}

// --- tail_log ---

type tailLog struct{ sb *sandbox.Sandbox }

func (t *tailLog) Name() string        { return "tail_log" }
func (t *tailLog) Description() string { return "Return the last N lines of a file (default 20)." }
func (t *tailLog) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "path", Type: tool.TypeString, Required: true, Description: "File path relative to the workspace root."},
		{Name: "lines", Type: tool.TypeInt, Default: int64(20), Description: "Number of trailing lines to return."},
	}}
}
func (t *tailLog) Invoke(_ context.Context, a tool.Args) (string, error) {
	abs, err := t.sb.Resolve(a.String("path"))
	if err != nil {
		return "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Only read the trailing window, so a multi-GB log doesn't come into RAM.
	win := int64(t.sb.MaxOutput)
	if win <= 0 {
		win = 1 << 20
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > win {
		if _, err := f.Seek(fi.Size()-win, io.SeekStart); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	n := int(a.Int("lines"))
	if n <= 0 {
		n = 20
	}
	if n < len(lines) {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// --- write_file ---

type writeFile struct{ sb *sandbox.Sandbox }

func (t *writeFile) Name() string { return "write_file" }
func (t *writeFile) Description() string {
	return "Write (create or overwrite) a text file in the workspace. Used to apply fixes."
}
func (t *writeFile) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "path", Type: tool.TypeString, Required: true, Description: "File path relative to the workspace root."},
		{Name: "content", Type: tool.TypeString, Required: true, Description: "Full new contents of the file."},
	}}
}
func (t *writeFile) Invoke(_ context.Context, a tool.Args) (string, error) {
	abs, err := t.sb.Resolve(a.String("path"))
	if err != nil {
		return "", err
	}
	content := a.String("content")
	if t.sb.DryRun {
		return fmt.Sprintf("[dry-run] would write %d bytes to %s", len(content), a.String("path")), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), a.String("path")), nil
}

// --- run_cmd ---

type runCmd struct{ sb *sandbox.Sandbox }

func (t *runCmd) Name() string { return "run_cmd" }
func (t *runCmd) Description() string {
	return "Run an allowlisted command in the workspace and return its combined output."
}
func (t *runCmd) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "cmd", Type: tool.TypeString, Required: true, Description: "Command name (must be allowlisted)."},
		{Name: "args", Type: tool.TypeStringArray, Description: "Command arguments."},
	}}
}
func (t *runCmd) Invoke(ctx context.Context, a tool.Args) (string, error) {
	return t.sb.Run(ctx, a.String("cmd"), a.StringArray("args"))
}

// --- http_probe ---

type httpProbe struct{ sb *sandbox.Sandbox }

func (t *httpProbe) Name() string { return "http_probe" }
func (t *httpProbe) Description() string {
	return "Send an HTTP GET to a URL and report the status code and a body snippet."
}
func (t *httpProbe) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "url", Type: tool.TypeString, Required: true, Description: "URL to probe, e.g. http://127.0.0.1:8080/health"},
	}}
}
func (t *httpProbe) Invoke(ctx context.Context, a tool.Args) (string, error) {
	url := a.String("url")
	client := &http.Client{Timeout: t.sb.Timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("bad url: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", tool.Retryable(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()

	snippet := make([]byte, 512)
	n, _ := resp.Body.Read(snippet)
	return fmt.Sprintf("HTTP %d %s\n%s", resp.StatusCode, resp.Status, strings.TrimSpace(string(snippet[:n]))), nil
}

// --- check_port ---

type checkPort struct{ sb *sandbox.Sandbox }

func (t *checkPort) Name() string { return "check_port" }
func (t *checkPort) Description() string {
	return "Check whether a TCP port is open (accepting connections)."
}
func (t *checkPort) Schema() tool.Schema {
	return tool.Schema{Params: []tool.Param{
		{Name: "host", Type: tool.TypeString, Default: "127.0.0.1", Description: "Host to connect to."},
		{Name: "port", Type: tool.TypeInt, Required: true, Description: "TCP port number."},
	}}
}
func (t *checkPort) Invoke(ctx context.Context, a tool.Args) (string, error) {
	host := a.String("host")
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", a.Int("port")))
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Sprintf("port %s is CLOSED (%v)", addr, err), nil
	}
	_ = conn.Close()
	return fmt.Sprintf("port %s is OPEN", addr), nil
}
