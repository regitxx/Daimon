"""DAIMON_HOME and socket-path resolution.

Mirrors internal/daimonhome/daimonhome.go so the Python SDK and the Go
binaries cannot disagree about where the daemon's socket lives.

Resolution order (matches Go):
  1. ``$DAIMON_HOME`` if set
  2. Platform default under the user's config dir, suffixed with ``daimon``:
     - macOS:   ``~/Library/Application Support/daimon``
     - Linux:   ``$XDG_CONFIG_HOME/daimon`` (default ``~/.config/daimon``)
     - Windows: ``%APPDATA%/daimon``

Socket path:
  ``$DAIMON_HOME/daimon.sock`` — with a transparent fallback to
  ``$TMPDIR/daimon-<uid>.sock`` when the primary path exceeds the
  AF_UNIX ``sun_path`` cap (104 bytes — the conservative darwin limit).
"""

from __future__ import annotations

import os
import platform
import sys
from pathlib import Path

ENV_VAR = "DAIMON_HOME"
DIR_NAME = "daimon"
SOCKET_NAME = "daimon.sock"
KEYSTORE_NAME = "identity.keystore"

# Conservative AF_UNIX sun_path cap. macOS: 104, Linux: 108. Use the lower
# bound so the same path works everywhere (matches sunPathLimit in Go).
_SUN_PATH_LIMIT = 104


def resolve_home() -> Path:
    """Return the absolute path to ``$DAIMON_HOME``.

    The directory is created at mode 0700 if it does not exist. If it
    exists with broader permissions, those are left alone — we don't
    tighten what the user chose.
    """
    env = os.environ.get(ENV_VAR)
    if env:
        home = Path(env).resolve()
    else:
        home = _platform_default()
    _ensure_dir(home)
    return home


def socket_path(home: Path) -> tuple[Path, bool]:
    """Return ``(path, fallback_used)`` for the daemon socket.

    If ``home/daimon.sock`` fits inside the conservative ``sun_path``
    cap, returns that with ``fallback_used=False``. Otherwise falls back
    to ``$TMPDIR/daimon-<uid>.sock`` and returns ``fallback_used=True``.
    Mirrors daimonhome.SocketPath.
    """
    primary = home / SOCKET_NAME
    if len(str(primary).encode()) <= _SUN_PATH_LIMIT:
        return primary, False
    alt = _tmp_fallback()
    if len(str(alt).encode()) > _SUN_PATH_LIMIT:
        raise RuntimeError(
            f"socket path too long for AF_UNIX (primary={len(str(primary))} > "
            f"{_SUN_PATH_LIMIT}, $TMPDIR fallback also too long): "
            f"set {ENV_VAR} to a shorter path"
        )
    return alt, True


def _platform_default() -> Path:
    home = Path.home()
    system = platform.system()
    if system == "Darwin":
        return home / "Library" / "Application Support" / DIR_NAME
    if system == "Windows":
        appdata = os.environ.get("APPDATA")
        if appdata:
            return Path(appdata) / DIR_NAME
        return home / "AppData" / "Roaming" / DIR_NAME
    # Linux + other Unix: XDG_CONFIG_HOME falls back to ~/.config.
    xdg = os.environ.get("XDG_CONFIG_HOME")
    if xdg:
        return Path(xdg) / DIR_NAME
    return home / ".config" / DIR_NAME


def _tmp_fallback() -> Path:
    tmp = Path(os.environ.get("TMPDIR") or "/tmp")
    tmp.mkdir(parents=True, exist_ok=True)
    if sys.platform.startswith("win"):
        tag = os.environ.get("USERNAME") or "default"
    else:
        tag = str(os.getuid())
    return tmp / f"daimon-{tag}.sock"


def _ensure_dir(path: Path) -> None:
    if path.exists():
        if not path.is_dir():
            raise RuntimeError(f"{path} exists and is not a directory")
        return
    path.mkdir(parents=True, mode=0o700, exist_ok=True)
