"""DAIMON_HOME / socket-path resolution mirrors the Go side."""

from __future__ import annotations

import os
import platform
from pathlib import Path

import pytest

from daimon import _home


def test_resolve_home_honors_env_var(short_tmp: Path, monkeypatch: pytest.MonkeyPatch):
    target = short_tmp / "explicit-home"
    monkeypatch.setenv(_home.ENV_VAR, str(target))
    got = _home.resolve_home()
    assert got == target.resolve()
    assert got.is_dir()
    if platform.system() != "Windows":
        # Mode 0o700 — keystore + sockets must not be world-readable.
        assert (got.stat().st_mode & 0o777) == 0o700


def test_resolve_home_creates_directory_when_missing(
    short_tmp: Path, monkeypatch: pytest.MonkeyPatch
):
    target = short_tmp / "nested" / "home"
    assert not target.exists()
    monkeypatch.setenv(_home.ENV_VAR, str(target))
    got = _home.resolve_home()
    assert got.is_dir()


def test_resolve_home_rejects_when_path_is_a_file(
    short_tmp: Path, monkeypatch: pytest.MonkeyPatch
):
    target = short_tmp / "not-a-dir"
    target.write_text("oops")
    monkeypatch.setenv(_home.ENV_VAR, str(target))
    with pytest.raises(RuntimeError, match="not a directory"):
        _home.resolve_home()


def test_socket_path_returns_primary_when_short(short_tmp: Path):
    home = short_tmp / "h"
    home.mkdir()
    path, fallback = _home.socket_path(home)
    assert path == home / _home.SOCKET_NAME
    assert fallback is False


def test_socket_path_falls_back_when_path_too_long(
    short_tmp: Path, monkeypatch: pytest.MonkeyPatch
):
    # Build a home path long enough that home/daimon.sock blows past the
    # 104-byte sun_path cap. The fallback into TMPDIR/daimon-<uid>.sock
    # must be returned with fallback=True.
    deep = short_tmp
    while len(str(deep / _home.SOCKET_NAME).encode()) <= 104:
        deep = deep / ("x" * 16)
        deep.mkdir(exist_ok=True)
    monkeypatch.setenv("TMPDIR", str(short_tmp))  # keep the alt short
    path, fallback = _home.socket_path(deep)
    assert fallback is True
    assert "daimon-" in path.name
    assert path.suffix == ".sock"
