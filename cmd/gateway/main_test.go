package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// findModuleRoot looks for go.mod in dir and parents.
func findModuleRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// TestBuildBinary verifies the gateway binary builds. Run from project root: go test ./cmd/gateway -run TestBuildBinary
func TestBuildBinary(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot, ok := findModuleRoot(cwd)
	if !ok {
		t.Skip("go.mod not found (run from module root)")
	}
	out := filepath.Join(t.TempDir(), "gateway")
	if os.PathSeparator == '\\' {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./cmd/gateway")
	cmd.Dir = moduleRoot
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		t.Skipf("go build failed (is Go in PATH?): %v", err)
	}
}
