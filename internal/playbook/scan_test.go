package playbook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Atishay8192261/praxis/internal/sandbox"
	"github.com/Atishay8192261/praxis/internal/tool"
	"github.com/Atishay8192261/praxis/internal/tools/devops"
)

func TestRunScan(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte("listen 9090;\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.log"), []byte("ok\nERROR: connection refused\n"), 0o644)

	sb, err := sandbox.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	devops.Register(reg, sb)

	var started []string
	obs := func(id, phase string) {
		if phase == "start" {
			started = append(started, id)
		}
	}

	report, path, err := RunScan(context.Background(), reg, obs)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := "survey>inspect>scan_errors>report"; strings.Join(path, ">") != want {
		t.Fatalf("path = %v, want %s", path, want)
	}
	if strings.Join(started, ">") != strings.Join(path, ">") {
		t.Fatalf("observer order %v != path %v", started, path)
	}
	if !strings.Contains(report, "nginx.conf") || !strings.Contains(report, "app.log") {
		t.Fatalf("report missing files:\n%s", report)
	}
	if !strings.Contains(strings.ToLower(report), "refused") {
		t.Fatalf("report missing error indicator:\n%s", report)
	}
}
