package devops

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Atishay8192261/praxis/internal/core"
	"github.com/Atishay8192261/praxis/internal/sandbox"
	"github.com/Atishay8192261/praxis/internal/tool"
)

func setup(t *testing.T) (*tool.Registry, *sandbox.Sandbox, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte("listen 9090;\nroot /var/www;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte("l1\nl2\nERROR bind: address in use\nl4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb, err := sandbox.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	Register(reg, sb)
	return reg, sb, dir
}

func dispatch(t *testing.T, reg *tool.Registry, name string, args map[string]any) core.Observation {
	t.Helper()
	return reg.Dispatch(context.Background(), core.ToolCall{ID: "c", Name: name, Args: args})
}

func TestReadListGrepTail(t *testing.T) {
	reg, _, _ := setup(t)

	if obs := dispatch(t, reg, "read_file", map[string]any{"path": "nginx.conf"}); obs.Failed() || !strings.Contains(obs.Output, "listen 9090") {
		t.Fatalf("read_file: %+v", obs)
	}
	if obs := dispatch(t, reg, "list_dir", map[string]any{"path": "."}); obs.Failed() || !strings.Contains(obs.Output, "nginx.conf") {
		t.Fatalf("list_dir: %+v", obs)
	}
	if obs := dispatch(t, reg, "grep", map[string]any{"pattern": "listen (\\d+)", "path": "."}); obs.Failed() || !strings.Contains(obs.Output, "nginx.conf") {
		t.Fatalf("grep: %+v", obs)
	}
	if obs := dispatch(t, reg, "tail_log", map[string]any{"path": "app.log", "lines": 2}); obs.Failed() || !strings.Contains(obs.Output, "l4") || strings.Contains(obs.Output, "l1") {
		t.Fatalf("tail_log: %+v", obs)
	}
}

func TestWriteFileAndDryRun(t *testing.T) {
	reg, sb, dir := setup(t)

	obs := dispatch(t, reg, "write_file", map[string]any{"path": "nginx.conf", "content": "listen 8080;\n"})
	if obs.Failed() {
		t.Fatalf("write_file: %+v", obs)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "nginx.conf"))
	if !strings.Contains(string(got), "8080") {
		t.Fatalf("file not updated: %q", got)
	}

	sb.DryRun = true
	obs = dispatch(t, reg, "write_file", map[string]any{"path": "nginx.conf", "content": "listen 1;\n"})
	if obs.Failed() || !strings.HasPrefix(obs.Output, "[dry-run]") {
		t.Fatalf("dry-run write: %+v", obs)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "nginx.conf"))
	if strings.Contains(string(got), "listen 1;") {
		t.Fatal("dry-run must not modify the file")
	}
}

func TestEscapeIsRejected(t *testing.T) {
	reg, _, _ := setup(t)
	obs := dispatch(t, reg, "read_file", map[string]any{"path": "../../../etc/passwd"})
	if !obs.Failed() {
		t.Fatalf("expected escape rejection, got %+v", obs)
	}
}

func TestHTTPProbeAndCheckPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	reg, _, _ := setup(t)

	obs := dispatch(t, reg, "http_probe", map[string]any{"url": srv.URL})
	if obs.Failed() || !strings.Contains(obs.Output, "HTTP 200") {
		t.Fatalf("http_probe: %+v", obs)
	}

	// Bind a port, then check it reports OPEN; a random unused port reports CLOSED.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	obs = dispatch(t, reg, "check_port", map[string]any{"host": "127.0.0.1", "port": portStr})
	if obs.Failed() || !strings.Contains(obs.Output, "OPEN") {
		t.Fatalf("check_port open: %+v", obs)
	}
}

func TestUnknownToolAndBadArgs(t *testing.T) {
	reg, _, _ := setup(t)
	if obs := dispatch(t, reg, "does_not_exist", nil); !obs.Failed() {
		t.Fatal("expected unknown tool error")
	}
	if obs := dispatch(t, reg, "read_file", map[string]any{}); !obs.Failed() {
		t.Fatal("expected missing-arg error")
	}
}
