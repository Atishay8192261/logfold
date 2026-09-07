package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveJail(t *testing.T) {
	dir := t.TempDir()
	sb, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ok, err := sb.Resolve("sub/file.txt")
	if err != nil {
		t.Fatalf("resolve within root: %v", err)
	}
	if !filepath.IsAbs(ok) {
		t.Fatalf("expected absolute path, got %q", ok)
	}

	for _, bad := range []string{"../escape", "sub/../../escape", "/etc/passwd"} {
		if _, err := sb.Resolve(bad); err == nil {
			t.Fatalf("expected escape error for %q", bad)
		}
	}
}

func TestRunAllowlistAndDryRun(t *testing.T) {
	dir := t.TempDir()
	sb, err := New(dir)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if _, err := sb.Run(context.Background(), "rm", []string{"-rf", "/"}); err == nil {
		t.Fatal("expected non-allowlisted command to be rejected")
	}

	sb.Allow = []string{"echo"}
	sb.DryRun = true
	out, err := sb.Run(context.Background(), "echo", []string{"hi"})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if want := "[dry-run]"; out[:len(want)] != want {
		t.Fatalf("expected dry-run description, got %q", out)
	}

	sb.DryRun = false
	out, err = sb.Run(context.Background(), "echo", []string{"hello"})
	if err != nil {
		t.Fatalf("run echo: %v", err)
	}
	if got := out; got == "" || got[:5] != "hello" {
		t.Fatalf("echo output = %q", got)
	}
}

func TestNewRejectsNonDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(f); err == nil {
		t.Fatal("expected error for non-directory root")
	}
}
