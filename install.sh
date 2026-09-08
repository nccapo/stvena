#!/bin/sh

set -eu

repo="nccapo/stvena"
requested_version="${STVENA_VERSION:-latest}"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *)
    echo "stvena: unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "stvena: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

if [ "$requested_version" = "latest" ]; then
  download_url="https://github.com/$repo/releases/latest/download"
else
  case "$requested_version" in
    v*) release_tag="$requested_version" ;;
    *) release_tag="v$requested_version" ;;
  esac
  download_url="https://github.com/$repo/releases/download/$release_tag"
fi

archive="stvena_${os}_${arch}.tar.gz"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/stvena-install.XXXXXX")"
trap 'rm -rf "$temp_dir"' EXIT HUP INT TERM

echo "Downloading stvena for $os/$arch..."
curl -fsSL --retry 3 "$download_url/$archive" -o "$temp_dir/$archive"
curl -fsSL --retry 3 "$download_url/checksums.txt" -o "$temp_dir/checksums.txt"

expected="$(awk -v file="$archive" '$2 == file { print $1 }' "$temp_dir/checksums.txt")"
if [ -z "$expected" ]; then
  echo "stvena: release checksum for $archive was not found" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$temp_dir/$archive" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$temp_dir/$archive" | awk '{ print $1 }')"
else
  echo "stvena: sha256sum or shasum is required to verify the download" >&2
  exit 1
fi

if [ "$actual" != "$expected" ]; then
  echo "stvena: checksum verification failed" >&2
  exit 1
fi

tar -xzf "$temp_dir/$archive" -C "$temp_dir"

install_dir="${STVENA_INSTALL_DIR:-}"
if [ -z "$install_dir" ]; then
  current_binary="$(command -v stvena 2>/dev/null || true)"
  if [ -n "$current_binary" ] && [ -w "$(dirname "$current_binary")" ]; then
    install_dir="$(dirname "$current_binary")"
  elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    install_dir="/usr/local/bin"
  elif [ -d /opt/homebrew/bin ] && [ -w /opt/homebrew/bin ]; then
    install_dir="/opt/homebrew/bin"
  else
    install_dir="${HOME}/.local/bin"
  fi
fi

mkdir -p "$install_dir"
if [ ! -w "$install_dir" ]; then
  echo "stvena: $install_dir is not writable" >&2
  echo "Set STVENA_INSTALL_DIR to a directory you can write to and run again." >&2
  exit 1
fi

install -m 0755 "$temp_dir/stvena" "$install_dir/stvena"
echo "Installed $("$install_dir/stvena" --version) to $install_dir/stvena"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    echo "Add $install_dir to your PATH, then open a new terminal:"
    echo "  export PATH=\"$install_dir:\$PATH\""
    ;;
esac
