//go:build unix

package logdup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")

	cleanup, err := Setup(path)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Fprintln(os.Stdout, "x")
	fmt.Fprintln(os.Stderr, "y")

	cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "x\n") || !strings.Contains(s, "y\n") {
		t.Fatalf("log file missing expected lines: %q", s)
	}
}
