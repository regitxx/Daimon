package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/regitxx/Daimon/internal/daimonhome"
)

// dialOrSpawn attempts to dial the Unix socket; if nothing is listening, it
// spawns a detached `daimond serve` process and polls until the new daemon
// publishes its socket. Returns an open connection ready for one or more RPC
// calls.
//
// Spawning rules:
//
//   - We only spawn when the dial fails with the system's "no listener"
//     signal (ECONNREFUSED on a stale socket file, ENOENT when the socket
//     doesn't exist yet). Other dial errors propagate — we don't second-guess
//     a permission error or path-too-long.
//
//   - The spawned process is fully detached (own session via Setsid),
//     stdout/stderr redirected to $DAIMON_HOME/daimon.log, stdin closed.
//     If the CLI exits, the daemon survives — that's the whole point.
//
//   - We poll the socket for up to ~5s with bounded backoff. If the daemon
//     doesn't publish in that window, we error out with a hint to inspect
//     the log file (the spawn may have failed for a reason we can't see).
func dialOrSpawn(home, socket string) (net.Conn, error) {
	// Fast path: socket exists, daemon is up.
	if c, err := dialOnce(socket); err == nil {
		return c, nil
	} else if !isSpawnableMiss(err) {
		return nil, err
	}

	// Spawn a detached daimond serve. The path resolution mirrors `which`:
	// PATH first (so a deployed install works), then a sibling-of-CLI
	// fallback (so `bin/daimon` next to `bin/daimond` in the dev tree works
	// without setting PATH).
	if err := spawnDaemon(home); err != nil {
		return nil, fmt.Errorf("spawn daimond: %w", err)
	}

	// Poll the socket. Bounded backoff: 50ms, 100ms, 200ms, 400ms, capped at
	// 1s thereafter, until the 5s wall clock.
	deadline := time.Now().Add(5 * time.Second)
	delay := 50 * time.Millisecond
	for time.Now().Before(deadline) {
		time.Sleep(delay)
		if c, err := dialOnce(socket); err == nil {
			return c, nil
		}
		delay *= 2
		if delay > time.Second {
			delay = time.Second
		}
	}
	return nil, fmt.Errorf("daimond did not publish socket %s within 5s; check %s",
		socket, daimonhome.LogPath(home))
}

func dialOnce(socket string) (net.Conn, error) {
	return net.DialTimeout("unix", socket, 250*time.Millisecond)
}

// isSpawnableMiss reports whether a dial error means "no daemon is listening,
// it's safe to spawn one" rather than a permanent error like permission
// denied. ECONNREFUSED for a stale socket file and ENOENT for an absent file
// are both spawnable.
func isSpawnableMiss(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if errors.Is(err, syscall.ENOENT) {
		return true
	}
	// net.Dial wraps the OS error in *net.OpError → *os.SyscallError; the
	// errors.Is check above unwraps both. Also handle the "file not found"
	// surface that some platforms return as a path error.
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, syscall.ENOENT) {
		return true
	}
	return false
}

// spawnDaemon forks `daimond serve` as a detached process with stdout/stderr
// redirected to $DAIMON_HOME/daimon.log. Resolves the daimond binary by:
//
//  1. $DAIMOND_BIN env var (explicit override, useful for tests)
//  2. exec.LookPath("daimond") (deployed install)
//  3. <dir of daimon binary>/daimond (development tree)
func spawnDaemon(home string) error {
	bin, err := resolveDaimond()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(daimonhome.LogPath(home), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log %s: %w", daimonhome.LogPath(home), err)
	}
	// Note: the file handle is intentionally NOT closed here — the spawned
	// daemon inherits the fd and writes to it for its lifetime. We deliberately
	// leak this single fd in the parent CLI process (which exits seconds later
	// after the unlock RPC completes); closing here would close the daemon's
	// stderr too.

	cmd := exec.Command(bin, "serve")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ() // pass DAIMON_HOME, OLLAMA_HOST, API keys through
	// Setsid: detach from the controlling terminal and become the leader of
	// a new session, so a SIGHUP to the CLI's parent (e.g. closing the
	// terminal) doesn't take the daemon with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	// Release the child — we never Wait() on it. The kernel will reparent it
	// to init (pid 1) when the CLI exits.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release daimond pid %d: %w", cmd.Process.Pid, err)
	}
	return nil
}

func resolveDaimond() (string, error) {
	if env := os.Getenv("DAIMOND_BIN"); env != "" {
		return env, nil
	}
	if path, err := exec.LookPath("daimond"); err == nil {
		return path, nil
	}
	self, err := os.Executable()
	if err == nil {
		sibling := filepath.Join(filepath.Dir(self), "daimond")
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	return "", fmt.Errorf("could not locate `daimond` binary (set $DAIMOND_BIN, install on PATH, or place beside daimon)")
}
