package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte("listen 9090;\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.log"), []byte("ERROR connection refused on 127.0.0.1:8080\n"), 0o644)
	return dir
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := New(Options{Workspace: testWorkspace(t), Backend: "heuristic"})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(s.Handler())
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func TestInfoAndTools(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	var info map[string]any
	getJSON(t, ts.URL+"/api/info", &info)
	if info["backend"] != "heuristic" {
		t.Fatalf("backend = %v", info["backend"])
	}

	var tools []map[string]any
	getJSON(t, ts.URL+"/api/tools", &tools)
	found := false
	for _, tl := range tools {
		if tl["name"] == "read_file" {
			found = true
		}
	}
	if !found {
		t.Fatal("read_file tool not listed")
	}
}

func TestRunLifecycle(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	body := strings.NewReader(`{"goal":"diagnose why the web service is unreachable on port 8080","dry_run":true}`)
	res, err := http.Post(ts.URL+"/api/run", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	var started map[string]string
	json.NewDecoder(res.Body).Decode(&started)
	res.Body.Close()
	id := started["run_id"]
	if id == "" {
		t.Fatal("no run_id returned")
	}

	deadline := time.Now().Add(3 * time.Second)
	var state map[string]any
	for time.Now().Before(deadline) {
		getJSON(t, ts.URL+"/api/runs/"+id, &state)
		if s, _ := state["status"].(string); s == "success" || s == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state["status"] != "success" {
		t.Fatalf("run did not succeed: %v", state["status"])
	}
	if result, _ := state["result"].(string); !strings.Contains(strings.ToLower(result), "refused") {
		t.Fatalf("unexpected result: %v", state["result"])
	}
}

func TestScanEndpoint(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	res, err := http.Post(ts.URL+"/api/scan", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	report, _ := out["report"].(string)
	if !strings.Contains(report, "nginx.conf") {
		t.Fatalf("scan report missing files: %v", report)
	}
}

func TestServesUI(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "LogFold") {
		t.Fatal("index.html not served")
	}
}
