#!/bin/bash
# DeepThought CLI installer — the claude.ai install.sh pattern, backed by the
# rolling `edge` GitHub Release. One-liner:
#
#   curl -fsSL https://raw.githubusercontent.com/ualberta-rcg/deepthought/main/install.sh | bash
#
# Installs the single static binary into ~/.local/bin (no root, nothing
# outside $HOME), verifies the SHA256 when the checksum asset is present, and
# prints the PATH/first-run guidance. Override the install dir with
# DEEPTHOUGHT_INSTALL_DIR.

set -e

REPO="ualberta-rcg/deepthought"
CHANNEL="${DEEPTHOUGHT_CHANNEL:-edge}"
INSTALL_DIR="${DEEPTHOUGHT_INSTALL_DIR:-$HOME/.local/bin}"
BINARY_NAME="deepthought-cli"
BASE_URL="https://github.com/${REPO}/releases/download/${CHANNEL}"

# Refuse sudo-from-a-user: everything lands under $HOME (claude.ai pattern).
if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
    echo "Error: do not run this installer with sudo." >&2
    echo "It installs into your home directory and needs no root access." >&2
    exit 1
fi

# Downloader (curl or wget).
DOWNLOADER=""
if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
else
    echo "Either curl or wget is required but neither is installed" >&2
    exit 1
fi

download() { # url [output]
    if [ "$DOWNLOADER" = "curl" ]; then
        [ -n "${2:-}" ] && curl -fsSL -o "$2" "$1" || curl -fsSL "$1"
    else
        [ -n "${2:-}" ] && wget -q -O "$2" "$1" || wget -q -O - "$1"
    fi
}

# Platform: today's release lanes are linux/amd64 (the Go build is static, so
# glibc/musl don't matter). Anything else fails with a clear message.
case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) echo "darwin builds are not published yet (linux/amd64 only today)" >&2; exit 1 ;;
    *) echo "Unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    *) echo "Unsupported architecture: $(uname -m) (linux/amd64 only today)" >&2; exit 1 ;;
esac
[ "${os}-${arch}" = "linux-amd64" ] || { echo "no release lane for ${os}-${arch}" >&2; exit 1; }

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading DeepThought CLI (${CHANNEL})…"
download "$BASE_URL/$BINARY_NAME" "$TMP_DIR/$BINARY_NAME"

# Verify the SHA256 when the release carries SHA256SUMS (best effort with a
# hard failure on mismatch).
checksum=""
if download "$BASE_URL/SHA256SUMS" "$TMP_DIR/SHA256SUMS" 2>/dev/null; then
    expected=$(awk -v f="$BINARY_NAME" '$2 == f {print $1}' "$TMP_DIR/SHA256SUMS" 2>/dev/null || true)
    if [ -n "$expected" ]; then
        actual=$(sha256sum "$TMP_DIR/$BINARY_NAME" | cut -d' ' -f1)
        if [ "$actual" != "$expected" ]; then
            echo "Checksum mismatch: expected $expected, got $actual" >&2
            exit 1
        fi
        checksum=" (sha256 verified)"
    fi
fi

mkdir -p "$INSTALL_DIR"
chmod 0755 "$TMP_DIR/$BINARY_NAME"
mv "$TMP_DIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
INSTALLED="$INSTALL_DIR/$BINARY_NAME"

VERSION="$("$INSTALLED" --version 2>/dev/null || echo unknown)"
echo "Installed $INSTALLED$checksum"
echo "Version: $VERSION"

# Env: data dir + PATH guidance.
mkdir -p "$HOME/.deepthought"
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        echo ""
        echo "~/.local/bin is not in your PATH. Add it (then open a new shell):"
        echo '    echo '\''export PATH="$HOME/.local/bin:$PATH"'\'' >> ~/.bashrc'
        ;;
esac

echo ""
echo "Next: run 'deepthought-cli'. Review discovered providers or open Settings."
echo "Optional server sync: Settings → Server → Connection. No model is needed to start."
