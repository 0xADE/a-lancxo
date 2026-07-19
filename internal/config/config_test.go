package config

import (
	"os"
	"testing"
)

func TestIdxrcPathDefault(t *testing.T) {
	os.Unsetenv("ADE_CONFIG_HOME")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got := idxrcPath()
	want := home + "/.config/ade/indexd.rc"
	if got != want {
		t.Fatalf("idxrcPath() = %q, want %q", got, want)
	}
}

func TestIdxrcPathFromADEConfigHome(t *testing.T) {
	t.Setenv("ADE_CONFIG_HOME", "/custom/ade-debug")
	got := idxrcPath()
	want := "/custom/ade-debug/indexd.rc"
	if got != want {
		t.Fatalf("idxrcPath() = %q, want %q", got, want)
	}
}

func TestIdxrcPathExpandsTildeInADEConfigHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADE_CONFIG_HOME", "~/.config/ade-debug")
	got := idxrcPath()
	want := home + "/.config/ade-debug/indexd.rc"
	if got != want {
		t.Fatalf("idxrcPath() = %q, want %q", got, want)
	}
}
