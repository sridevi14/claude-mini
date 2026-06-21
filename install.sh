#!/bin/sh
# ============================================================
#  claude-mini installer for macOS and Linux
#  Downloads the matching binary from the latest GitHub Release
#  into ~/.local/bin and makes sure that folder is on your PATH.
#  (Windows users: run install.cmd instead.)
# ============================================================
set -eu

REPO="sridevi14/claude-mini"
BIN_NAME="claude-mini"
INSTALL_DIR="${CLAUDE_MINI_INSTALL_DIR:-$HOME/.local/bin}"

# --- 1. Detect OS -------------------------------------------
os="$(uname -s)"
case "$os" in
    Linux)  os="linux" ;;
    Darwin) os="darwin" ;;
    *)
        echo "Unsupported OS: $os"
        echo "This installer supports Linux and macOS. On Windows, use install.cmd."
        exit 1
        ;;
esac

# --- 2. Detect CPU architecture -----------------------------
arch="$(uname -m)"
case "$arch" in
    x86_64 | amd64)  arch="amd64" ;;
    arm64 | aarch64) arch="arm64" ;;
    *)
        echo "Unsupported architecture: $arch"
        exit 1
        ;;
esac

asset="claude-mini-${os}-${arch}"
url="https://github.com/${REPO}/releases/latest/download/${asset}"

echo "Installing claude-mini (${os}/${arch})..."

# --- 3. Download into the install dir -----------------------
mkdir -p "$INSTALL_DIR"
tmp="$(mktemp)"
echo "Downloading ${asset} from the latest release..."
if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$tmp"
elif command -v wget >/dev/null 2>&1; then
    wget -qO "$tmp" "$url"
else
    echo "ERROR: need either curl or wget installed."
    exit 1
fi

chmod +x "$tmp"
mv "$tmp" "$INSTALL_DIR/$BIN_NAME"
echo "Installed to $INSTALL_DIR/$BIN_NAME"

# --- 4. Make sure the install dir is on PATH ----------------
case ":$PATH:" in
    *":$INSTALL_DIR:"*)
        on_path=1 ;;
    *)
        on_path=0 ;;
esac

if [ "$on_path" -eq 0 ]; then
    line="export PATH=\"$INSTALL_DIR:\$PATH\""
    changed=""
    for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
        # Append to shell rc files that exist and don't already mention the dir.
        if [ -f "$rc" ] && ! grep -qsF "$INSTALL_DIR" "$rc"; then
            printf '\n# Added by claude-mini installer\n%s\n' "$line" >> "$rc"
            changed="$changed $rc"
        fi
    done
    # If no rc existed at all, create ~/.profile so login shells pick it up.
    if [ -z "$changed" ] && [ ! -f "$HOME/.zshrc" ] && [ ! -f "$HOME/.bashrc" ]; then
        printf '\n# Added by claude-mini installer\n%s\n' "$line" >> "$HOME/.profile"
        changed=" $HOME/.profile"
    fi
    echo "Added $INSTALL_DIR to your PATH in:$changed"
fi

echo
echo "============================================================"
echo "  claude-mini installed."
echo
if [ "$on_path" -eq 0 ]; then
    echo "  Open a NEW terminal (or run:  source ~/.profile )"
    echo "  then run it from any directory:"
else
    echo "  Run it from any directory:"
fi
echo
echo "      claude-mini"
echo
echo "  (re-run this installer any time to update to the latest release)"
echo "============================================================"
