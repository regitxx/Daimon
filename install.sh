#!/usr/bin/env sh
# Daimon installer — resolves the latest GitHub Release, detects your
# platform, downloads the matching tarball, verifies its SHA-256 against
# the published `checksums.txt`, and drops `daimon` + `daimond` somewhere
# on PATH.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/regitxx/Daimon/main/install.sh | sh
#
# Or, if you prefer to read scripts before executing them (recommended for
# any installer you don't already trust):
#   curl -fsSLO https://raw.githubusercontent.com/regitxx/Daimon/main/install.sh
#   less install.sh                    # inspect
#   sh install.sh
#
# Env vars (all optional):
#   DAIMON_INSTALL_PREFIX   target directory (default: /usr/local/bin if
#                           writable, else $HOME/.local/bin)
#   DAIMON_INSTALL_TAG      pin to a specific release tag instead of latest
#                           (e.g. DAIMON_INSTALL_TAG=v0.2.0-dev.3)
#   DAIMON_INCLUDE_MOCK     also install x402-mock-server (default: no;
#                           set to 1 to include — useful for local x402
#                           testing without a real facilitator)
#
# Exit codes:
#   0  installed cleanly
#   1  unsupported platform / missing dependency
#   2  download or checksum failure
#
# The installer makes ZERO modifications outside DAIMON_INSTALL_PREFIX. It
# does NOT touch your shell rc files, env vars, or daimon home. If the
# install prefix isn't on your PATH the script prints the export line you
# need to add yourself — you stay in control of your shell config.

set -eu

REPO="regitxx/Daimon"
PROG="Daimon installer"

# --- Platform detection -----------------------------------------------------

OS_RAW=$(uname -s)
ARCH_RAW=$(uname -m)

case "$OS_RAW" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux ;;
  *)
    echo "${PROG}: unsupported OS '$OS_RAW'" >&2
    echo "${PROG}: build from source instead: https://github.com/${REPO}#install" >&2
    exit 1
    ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *)
    echo "${PROG}: unsupported arch '$ARCH_RAW'" >&2
    echo "${PROG}: build from source instead: https://github.com/${REPO}#install" >&2
    exit 1
    ;;
esac

PLAT="${OS}-${ARCH}"

# --- Required tools ---------------------------------------------------------

# We need curl (or wget), tar, and one of shasum / sha256sum. Fail loudly
# if any are missing — better than a confusing error halfway through.
need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "${PROG}: required tool '$1' is not installed" >&2
    exit 1
  fi
}
need tar
if command -v curl >/dev/null 2>&1; then
  FETCH="curl -fsSL"
elif command -v wget >/dev/null 2>&1; then
  FETCH="wget -qO-"
else
  echo "${PROG}: need either curl or wget" >&2
  exit 1
fi
if command -v shasum >/dev/null 2>&1; then
  SHA="shasum -a 256"
elif command -v sha256sum >/dev/null 2>&1; then
  SHA="sha256sum"
else
  echo "${PROG}: need either shasum or sha256sum to verify the download" >&2
  exit 1
fi

# --- Resolve the release tag ------------------------------------------------

if [ "${DAIMON_INSTALL_TAG:-}" = "" ]; then
  # GitHub's /releases/latest skips pre-releases automatically, so for a
  # project that only has pre-releases (like Daimon's current state) we
  # need to fall back to /releases and pick the most recent one. We try
  # /latest first because once a real GA exists this is the right
  # answer; the fallback is just for the bootstrap period.
  TAG=$($FETCH "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
        | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 || true)
  if [ -z "$TAG" ]; then
    TAG=$($FETCH "https://api.github.com/repos/${REPO}/releases" 2>/dev/null \
          | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1 || true)
  fi
else
  TAG="$DAIMON_INSTALL_TAG"
fi

if [ -z "$TAG" ]; then
  echo "${PROG}: could not resolve a release tag from https://api.github.com/repos/${REPO}/releases" >&2
  exit 2
fi

echo "${PROG}: target release = $TAG"
echo "${PROG}: target platform = $PLAT"

# --- Install prefix ---------------------------------------------------------

if [ -n "${DAIMON_INSTALL_PREFIX:-}" ]; then
  PREFIX="$DAIMON_INSTALL_PREFIX"
elif [ -w /usr/local/bin ] 2>/dev/null; then
  PREFIX=/usr/local/bin
else
  PREFIX="$HOME/.local/bin"
fi
mkdir -p "$PREFIX"
echo "${PROG}: install prefix  = $PREFIX"
echo

# --- Download + verify ------------------------------------------------------

TARBALL="daimon-${TAG}-${PLAT}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${TARBALL}"
SUMS_URL="https://github.com/${REPO}/releases/download/${TAG}/checksums.txt"

TMP=$(mktemp -d 2>/dev/null || mktemp -d -t daimon-install)
trap 'rm -rf "$TMP"' EXIT

echo "${PROG}: downloading $TARBALL"
if ! $FETCH "$URL" > "$TMP/$TARBALL" 2>/dev/null; then
  echo "${PROG}: failed to download $URL" >&2
  echo "${PROG}: check that the release + platform tarball exist at:" >&2
  echo "                https://github.com/${REPO}/releases/tag/${TAG}" >&2
  exit 2
fi

echo "${PROG}: downloading checksums.txt"
$FETCH "$SUMS_URL" > "$TMP/checksums.txt"

# Match the line by tarball name + check the SHA-256 against the
# downloaded bytes. shasum and sha256sum agree on output format
# (HEX SP SP NAME), so the cut field is the same on both.
EXPECTED=$(grep "  ${TARBALL}\$" "$TMP/checksums.txt" | awk '{print $1}')
GOT=$($SHA "$TMP/$TARBALL" | awk '{print $1}')

if [ -z "$EXPECTED" ]; then
  echo "${PROG}: ${TARBALL} not listed in checksums.txt — refusing to install" >&2
  exit 2
fi
if [ "$EXPECTED" != "$GOT" ]; then
  echo "${PROG}: SHA-256 MISMATCH — refusing to install" >&2
  echo "  expected: $EXPECTED" >&2
  echo "  got:      $GOT" >&2
  exit 2
fi
echo "${PROG}: SHA-256 verified ($EXPECTED)"

# --- Extract + install ------------------------------------------------------

tar -C "$TMP" -xzf "$TMP/$TARBALL"
STAGE="$TMP/daimon-${TAG}-${PLAT}"

if [ ! -x "$STAGE/daimon" ] || [ ! -x "$STAGE/daimond" ]; then
  echo "${PROG}: tarball is missing daimon or daimond binary" >&2
  exit 2
fi

# Use `install` if available (preserves +x mode + atomic move); fall back
# to cp + chmod on systems where `install` isn't on PATH.
copy_bin() {
  src="$1"
  dst="$2"
  if command -v install >/dev/null 2>&1; then
    install -m 0755 "$src" "$dst"
  else
    cp "$src" "$dst"
    chmod 0755 "$dst"
  fi
}

copy_bin "$STAGE/daimon"  "$PREFIX/daimon"
copy_bin "$STAGE/daimond" "$PREFIX/daimond"
if [ "${DAIMON_INCLUDE_MOCK:-}" = "1" ] && [ -x "$STAGE/x402-mock-server" ]; then
  copy_bin "$STAGE/x402-mock-server" "$PREFIX/x402-mock-server"
  echo "${PROG}: installed x402-mock-server (DAIMON_INCLUDE_MOCK=1)"
fi

INSTALLED_VERSION=$("$PREFIX/daimon" --help 2>&1 | head -1 || echo "(unknown)")

echo
echo "${PROG}: installed:"
echo "  $PREFIX/daimon"
echo "  $PREFIX/daimond"
echo
echo "  version: $INSTALLED_VERSION"
echo

# --- PATH check + next steps ------------------------------------------------

# Probe whether the install prefix is already on the user's PATH. We do
# this via `command -v daimon` looking up to the prefix's path — if the
# resolved daimon is OURS, the PATH is good.
RESOLVED_DAIMON=$(command -v daimon 2>/dev/null || true)
if [ "$RESOLVED_DAIMON" = "$PREFIX/daimon" ]; then
  echo "${PROG}: $PREFIX is on your PATH — \`daimon\` is ready to use."
else
  echo "${PROG}: $PREFIX is NOT on your PATH."
  echo "  Add this to your shell rc file (~/.bashrc, ~/.zshrc, etc.):"
  echo "    export PATH=\"$PREFIX:\$PATH\""
  echo "  Then re-open the shell or run \`exec \$SHELL\`."
fi

echo
echo "Next: \`daimon init\` to provision your identity."
echo "QUICKSTART: https://github.com/${REPO}/blob/main/QUICKSTART.md"
