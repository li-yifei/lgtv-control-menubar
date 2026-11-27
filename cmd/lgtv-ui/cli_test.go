package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLegacyCLIStatusThroughBundleLauncher(t *testing.T) {
	args, cli := cliArgs([]string{"--cli", "--config", "/tmp/tv.json", "--status"}, false)
	want := []string{"--config", "/tmp/tv.json", "status", "--json"}
	if !cli || !slices.Equal(args, want) {
		t.Fatalf("legacy status arguments became %v, cli=%v", args, cli)
	}
}

func TestBundleVersionFlagUsesCLI(t *testing.T) {
	if _, cli := cliArgs([]string{"-v"}, false); !cli {
		t.Fatal("the bundle opened the GUI for -v")
	}
}

func TestUserCLIUpdateReplacesStaleCopyAndDanglingLink(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "LGTVControl")
	path := filepath.Join(dir, "lgtv")
	if err := os.WriteFile(executable, []byte("current"), 0700); err != nil {
		t.Fatal(err)
	}
	if state := cliInstallationAt(path, executable); state != cliMissing {
		t.Fatalf("missing CLI state = %v", state)
	}
	for _, setup := range []func() error{
		func() error { return os.WriteFile(path, []byte("old"), 0700) },
		func() error { return os.Symlink(filepath.Join(dir, "removed"), path) },
	} {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
		if cliInstallationAt(path, executable) != cliNeedsUpdate {
			t.Fatal("stale CLI was not offered an update")
		}
		if err := linkUserCLI(path, executable); err != nil {
			t.Fatal(err)
		}
		if cliInstallationAt(path, executable) != cliCurrent {
			t.Fatal("updated CLI did not resolve to the current app")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}
