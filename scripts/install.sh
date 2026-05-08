#!/usr/bin/env sh
set -eu

APP_NAME="kiro-gateway"
REPO="pinealctx/kiro-gateway"
EXPECTED_OS="__KIRO_GATEWAY_OS__"
EXPECTED_ARCH="__KIRO_GATEWAY_ARCH__"
OS_NAME=$(uname -s 2>/dev/null || echo unknown)
case "$OS_NAME" in
  Linux) CURRENT_OS="linux" ;;
  Darwin) CURRENT_OS="darwin" ;;
  *)
    echo "error: install.sh supports Linux and macOS only. Use install.ps1 on Windows." >&2
    exit 1
    ;;
esac

ARCH_NAME=$(uname -m 2>/dev/null || echo unknown)
case "$ARCH_NAME" in
  x86_64|amd64) CURRENT_ARCH="amd64" ;;
  arm64|aarch64) CURRENT_ARCH="arm64" ;;
  *) CURRENT_ARCH="$ARCH_NAME" ;;
esac

if [ "$EXPECTED_OS" != "__KIRO_GATEWAY_OS__" ] && [ "$EXPECTED_OS" != "$CURRENT_OS" ]; then
  echo "error: this archive is for ${EXPECTED_OS}/${EXPECTED_ARCH}, but this machine is ${CURRENT_OS}/${CURRENT_ARCH}" >&2
  exit 1
fi

if [ "$EXPECTED_ARCH" != "__KIRO_GATEWAY_ARCH__" ] && [ "$EXPECTED_ARCH" != "$CURRENT_ARCH" ]; then
  echo "error: this archive is for ${EXPECTED_OS}/${EXPECTED_ARCH}, but this machine is ${CURRENT_OS}/${CURRENT_ARCH}" >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SRC="${SCRIPT_DIR}/${APP_NAME}"
DEST_DIR="${INSTALL_DIR:-/usr/local/bin}"
DEST="${DEST_DIR}/${APP_NAME}"

if [ ! -f "$SRC" ]; then
  DOWNLOAD_OS="$CURRENT_OS"
  DOWNLOAD_ARCH="$CURRENT_ARCH"
  if [ "$EXPECTED_OS" != "__KIRO_GATEWAY_OS__" ]; then
    DOWNLOAD_OS="$EXPECTED_OS"
  fi
  if [ "$EXPECTED_ARCH" != "__KIRO_GATEWAY_ARCH__" ]; then
    DOWNLOAD_ARCH="$EXPECTED_ARCH"
  fi

  TMP_DIR=$(mktemp -d)
  trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

  if command -v curl >/dev/null 2>&1; then
    LATEST_URL=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
    VERSION=${LATEST_URL##*/}
    ASSET="${APP_NAME}_${VERSION}_${DOWNLOAD_OS}_${DOWNLOAD_ARCH}.tar.gz"
    curl -fL "https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}" -o "${TMP_DIR}/${ASSET}"
  elif command -v wget >/dev/null 2>&1; then
    LATEST_URL=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
    VERSION=$LATEST_URL
    ASSET="${APP_NAME}_${VERSION}_${DOWNLOAD_OS}_${DOWNLOAD_ARCH}.tar.gz"
    wget -q "https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}" -O "${TMP_DIR}/${ASSET}"
  else
    echo "error: curl or wget is required to download ${APP_NAME}" >&2
    exit 1
  fi

  tar -xzf "${TMP_DIR}/${ASSET}" -C "$TMP_DIR"
  SRC="${TMP_DIR}/${APP_NAME}_${VERSION}_${DOWNLOAD_OS}_${DOWNLOAD_ARCH}/${APP_NAME}"
  if [ ! -f "$SRC" ]; then
    echo "error: ${APP_NAME} binary not found in downloaded archive" >&2
    exit 1
  fi
fi

mkdir -p "$DEST_DIR" 2>/dev/null || {
  echo "error: cannot create ${DEST_DIR}. Try: sudo INSTALL_DIR=${DEST_DIR} sh install.sh" >&2
  exit 1
}

cp "$SRC" "$DEST" 2>/dev/null || {
  echo "error: cannot write ${DEST}. Try: sudo INSTALL_DIR=${DEST_DIR} sh install.sh" >&2
  exit 1
}
chmod 755 "$DEST"

case ":$PATH:" in
  *":$DEST_DIR:"*) PATH_UPDATED="false" ;;
  *)
    PATH_UPDATED="true"
    SHELL_NAME=$(basename "${SHELL:-}")
    if [ "$OS_NAME" = "Darwin" ] && [ "$SHELL_NAME" = "zsh" ]; then
      PROFILE="${HOME}/.zshrc"
    elif [ "$SHELL_NAME" = "bash" ]; then
      PROFILE="${HOME}/.bashrc"
    elif [ "$SHELL_NAME" = "zsh" ]; then
      PROFILE="${HOME}/.zshrc"
    else
      PROFILE="${HOME}/.profile"
    fi
    MARKER="# ${APP_NAME} PATH"
    if [ -w "$(dirname "$PROFILE")" ] && ! grep -F "$MARKER" "$PROFILE" >/dev/null 2>&1; then
      {
        echo ""
        echo "$MARKER"
        echo "export PATH=\"$DEST_DIR:\$PATH\""
      } >> "$PROFILE"
    fi
    ;;
esac

echo "Installed ${APP_NAME} to ${DEST}"
if [ "${PATH_UPDATED:-false}" = "true" ]; then
  echo "Added ${DEST_DIR} to ${PROFILE}. Restart your terminal or run: export PATH=\"${DEST_DIR}:\$PATH\""
fi
echo "Run: ${APP_NAME} --help"
