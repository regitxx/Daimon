package daimonhome_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/regitxx/Daimon/internal/daimonhome"
)

func TestResolve_RespectsEnvVar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(daimonhome.EnvVar, dir)
	got, err := daimonhome.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want, _ := filepath.Abs(dir)
	if got != want {
		t.Errorf("Resolve: got %q, want %q", got, want)
	}
}

func TestResolve_CreatesDirectoryIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "newhome")
	t.Setenv(daimonhome.EnvVar, dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s should not exist, stat err=%v", dir, err)
	}
	got, err := daimonhome.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("Resolve did not create dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("Resolve created a non-directory")
	}
	// Mode check skipped on Windows where 0700 doesn't apply.
}

func TestResolve_FallbackToUserConfigDir(t *testing.T) {
	// Unset DAIMON_HOME so the fallback path runs. We don't assert the exact
	// path (that depends on the test runner's home dir) but we do assert it
	// is non-empty and ends in our DirName.
	t.Setenv(daimonhome.EnvVar, "")
	got, err := daimonhome.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == "" {
		t.Fatal("Resolve returned empty path")
	}
	if filepath.Base(got) != daimonhome.DirName {
		t.Errorf("Resolve: got %q, want path ending in /%s", got, daimonhome.DirName)
	}
}

func TestResolve_RejectsNonDirectory(t *testing.T) {
	// Create a regular file at the resolved path; Resolve must error rather
	// than silently use it.
	dir := t.TempDir()
	bogus := filepath.Join(dir, "file-not-dir")
	if err := os.WriteFile(bogus, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(daimonhome.EnvVar, bogus)
	if _, err := daimonhome.Resolve(); err == nil {
		t.Fatal("Resolve: expected error when path is a regular file")
	}
}

func TestSocketPath_NormalCase(t *testing.T) {
	dir := t.TempDir()
	path, fallback, err := daimonhome.SocketPath(dir)
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	if fallback {
		t.Errorf("expected no fallback for short path; got fallback=%v path=%q", fallback, path)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("expected socket inside home dir; got %q", path)
	}
	if filepath.Base(path) != daimonhome.SocketName {
		t.Errorf("expected socket name %q; got %q", daimonhome.SocketName, filepath.Base(path))
	}
}

func TestSocketPath_FallsBackOnSunPathOverflow(t *testing.T) {
	// Construct a home path that, combined with /daimon.sock, exceeds the 104
	// byte sun_path cap.
	long := strings.Repeat("a", 130)
	home := "/" + long
	path, fallback, err := daimonhome.SocketPath(home)
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	if !fallback {
		t.Errorf("expected fallback to $TMPDIR; got fallback=%v path=%q", fallback, path)
	}
	if len(path) > 104 {
		t.Errorf("fallback path also overflows sun_path: len=%d path=%q", len(path), path)
	}
}

func TestKeystoreAndLogPaths(t *testing.T) {
	home := "/some/home"
	if got, want := daimonhome.KeystorePath(home), filepath.Join(home, daimonhome.KeystoreName); got != want {
		t.Errorf("KeystorePath: got %q, want %q", got, want)
	}
	if got, want := daimonhome.LogPath(home), filepath.Join(home, daimonhome.LogName); got != want {
		t.Errorf("LogPath: got %q, want %q", got, want)
	}
}
