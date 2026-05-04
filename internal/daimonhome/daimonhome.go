// Package daimonhome resolves the on-disk location of a principal's daimon
// state and the per-machine paths that derive from it.
//
// All filesystem layout decisions for cmd/daimond and cmd/daimon route through
// this package so the two binaries cannot disagree about where the keystore
// lives, where the socket should be opened, or where logs go. SPEC §10
// specifies $DAIMON_HOME as the root; v0.1 resolves it via:
//
//  1. $DAIMON_HOME if set (explicit user override)
//  2. os.UserConfigDir() / "daimon" otherwise — which gives:
//     - linux:  $XDG_CONFIG_HOME/daimon  (default ~/.config/daimon)
//     - darwin: ~/Library/Application Support/daimon
//     - windows: %AppData%/daimon
//
// SPEC §10 originally said XDG_DATA_HOME; this package follows
// os.UserConfigDir() instead — config is the closer fit for "config + secrets
// + sockets" and matches what the standard library gives us. The SPEC text
// has been amended to match.
//
// Resolve creates the directory at mode 0700 if it does not exist; we never
// write secrets into a world-readable directory.
package daimonhome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

const (
	// EnvVar is the explicit override for the home directory.
	EnvVar = "DAIMON_HOME"

	// DirName is the subdirectory created under os.UserConfigDir() when
	// EnvVar is unset.
	DirName = "daimon"

	// SocketName, KeystoreName, LogName are the standard files inside
	// $DAIMON_HOME. Stable across v0.1 releases — clients can hardcode these
	// names as long as they call this package's path helpers.
	SocketName   = "daimon.sock"
	KeystoreName = "identity.keystore"
	LogName      = "daimon.log"

	// sunPathLimit is the conservative cap for AF_UNIX socket path length.
	// macOS allows 104 bytes (including the trailing NUL); Linux allows 108.
	// We use the lower bound so the same path works everywhere.
	sunPathLimit = 104
)

// Resolve returns the absolute path to $DAIMON_HOME. The directory is created
// (mode 0700) if it does not exist; if it exists with broader permissions on a
// POSIX system, that is left alone (we don't tighten what the user chose).
func Resolve() (string, error) {
	if env := os.Getenv(EnvVar); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return "", fmt.Errorf("daimonhome: resolve %s=%q: %w", EnvVar, env, err)
		}
		if err := ensureDir(abs); err != nil {
			return "", err
		}
		return abs, nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("daimonhome: user config dir: %w", err)
	}
	home := filepath.Join(cfg, DirName)
	if err := ensureDir(home); err != nil {
		return "", err
	}
	return home, nil
}

// KeystorePath returns the encrypted keystore path inside home.
func KeystorePath(home string) string { return filepath.Join(home, KeystoreName) }

// LogPath returns the daemon log path inside home.
func LogPath(home string) string { return filepath.Join(home, LogName) }

// SocketPath returns the Unix socket path the daemon should listen on for
// the given home directory.
//
// AF_UNIX sun_path is capped at 104 bytes on darwin, 108 on linux. If
// home/daimon.sock exceeds the conservative cap, SocketPath transparently
// falls back to $TMPDIR/daimon-$uid.sock and signals the fallback via the
// returned bool — callers that want to surface a one-line warning can do so.
//
// Both binaries (daimond serve and the CLI) MUST call this same function;
// otherwise they would compute different paths and the CLI would dial a
// socket the daemon never opened.
func SocketPath(home string) (path string, fallback bool, err error) {
	primary := filepath.Join(home, SocketName)
	if len(primary) <= sunPathLimit {
		return primary, false, nil
	}
	alt, err := tmpFallback()
	if err != nil {
		return "", false, err
	}
	if len(alt) > sunPathLimit {
		return "", false, fmt.Errorf(
			"daimonhome: socket path too long for AF_UNIX (%d > %d) and $TMPDIR fallback also too long (%d): set %s to a shorter path",
			len(primary), sunPathLimit, len(alt), EnvVar,
		)
	}
	return alt, true, nil
}

// tmpFallback returns $TMPDIR/daimon-$uid.sock, creating $TMPDIR if needed.
// On Windows os.Getuid returns -1; we substitute the user's name so two users
// on the same machine still don't collide.
func tmpFallback() (string, error) {
	dir := os.TempDir()
	if err := ensureDir(dir); err != nil {
		return "", err
	}
	tag := "user"
	if uid := os.Getuid(); uid >= 0 {
		tag = strconv.Itoa(uid)
	} else if u := os.Getenv("USERNAME"); u != "" {
		tag = u
	}
	if runtime.GOOS == "windows" && tag == "user" {
		// AF_UNIX support exists on Windows 10+; the principle stands.
		tag = "default"
	}
	return filepath.Join(dir, "daimon-"+tag+".sock"), nil
}

// ensureDir creates path with mode 0700 if it does not exist. If it exists and
// is a directory, ensureDir is a no-op; if it exists and is not a directory,
// ensureDir errors.
func ensureDir(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("daimonhome: %s exists and is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daimonhome: stat %s: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("daimonhome: create %s: %w", path, err)
	}
	return nil
}
