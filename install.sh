#!/usr/bin/env bash
set -euo pipefail

REPO="dpasca/LittleControlRoom"
VERSION="${LCR_VERSION:-latest}"
INSTALL_DIR="${LCR_INSTALL_DIR:-${INSTALL_DIR:-$HOME/.local/bin}}"
EXPECTED_MACOS_TEAM_ID="69NH26W767"
EXPECTED_MACOS_AUTHORITY="Developer ID Application: NEWTYPE K.K. (69NH26W767)"

say() {
  printf '%s\n' "$*"
}

fail() {
  printf 'lcroom install: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

download() {
  local url="$1"
  local dest="$2"
  curl -fsSL "$url" -o "$dest"
}

verify_macos_binary() {
  local bin="$1"
  local name
  local details
  local assessment

  name="$(basename "$bin")"
  codesign --verify --strict --verbose=2 "$bin" >/dev/null 2>&1 ||
    fail "${name} is not a valid strict macOS code-signed binary"

  details="$(codesign -dv "$bin" 2>&1)"
  printf '%s\n' "$details" | grep -Fq "TeamIdentifier=${EXPECTED_MACOS_TEAM_ID}" ||
    fail "${name} is not signed by the expected Apple Developer Team ID ${EXPECTED_MACOS_TEAM_ID}"
  printf '%s\n' "$details" | grep -Fq "Authority=${EXPECTED_MACOS_AUTHORITY}" ||
    fail "${name} is not signed by ${EXPECTED_MACOS_AUTHORITY}"

  assessment="$(spctl --assess --type execute --verbose=4 "$bin" 2>&1)" ||
    fail "${name} failed macOS Gatekeeper assessment: ${assessment}"
}

need_cmd curl
need_cmd awk
need_cmd mktemp

os="$(uname -s)"
machine="$(uname -m)"

case "$os" in
  Darwin)
    os_name="Darwin"
    archive_ext="zip"
    need_cmd unzip
    need_cmd shasum
    need_cmd codesign
    need_cmd spctl
    ;;
  Linux)
    os_name="Linux"
    archive_ext="tar.gz"
    need_cmd tar
    need_cmd sha256sum
    ;;
  *)
    fail "unsupported operating system: ${os}"
    ;;
esac

case "$machine" in
  arm64 | aarch64)
    arch_name="arm64"
    ;;
  x86_64 | amd64)
    arch_name="x86_64"
    ;;
  *)
    fail "unsupported CPU architecture: ${machine}"
    ;;
esac

asset="lcroom_${os_name}_${arch_name}.${archive_ext}"
if [[ "$VERSION" == "latest" ]]; then
  release_base="https://github.com/${REPO}/releases/latest/download"
else
  release_base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

archive_path="${tmp_dir}/${asset}"
checksums_path="${tmp_dir}/checksums.txt"
unpack_dir="${tmp_dir}/unpack"
mkdir -p "$unpack_dir"

say "Downloading ${asset} from ${REPO} ${VERSION}"
download "${release_base}/${asset}" "$archive_path" ||
  fail "could not download ${release_base}/${asset}"
download "${release_base}/checksums.txt" "$checksums_path" ||
  fail "could not download ${release_base}/checksums.txt"

expected_checksum="$(awk -v asset="$asset" '$2 == asset { print $1 }' "$checksums_path")"
[[ -n "$expected_checksum" ]] || fail "checksums.txt does not contain ${asset}"

case "$os_name" in
  Darwin)
    actual_checksum="$(shasum -a 256 "$archive_path" | awk '{ print $1 }')"
    ;;
  Linux)
    actual_checksum="$(sha256sum "$archive_path" | awk '{ print $1 }')"
    ;;
esac

[[ "$actual_checksum" == "$expected_checksum" ]] ||
  fail "checksum mismatch for ${asset}"
say "Verified SHA256 checksum"

case "$archive_ext" in
  zip)
    unzip -q "$archive_path" -d "$unpack_dir"
    ;;
  tar.gz)
    tar -xzf "$archive_path" -C "$unpack_dir"
    ;;
esac

for bin in lcroom lcagent; do
  [[ -f "${unpack_dir}/${bin}" ]] || fail "archive does not contain ${bin}"
  chmod 0755 "${unpack_dir}/${bin}"
done

if [[ "$os_name" == "Darwin" ]]; then
  verify_macos_binary "${unpack_dir}/lcroom"
  verify_macos_binary "${unpack_dir}/lcagent"
  say "Verified macOS code signatures and Gatekeeper assessment"
fi

mkdir -p "$INSTALL_DIR" || fail "could not create install directory: ${INSTALL_DIR}"
[[ -d "$INSTALL_DIR" ]] || fail "install path is not a directory: ${INSTALL_DIR}"
[[ -w "$INSTALL_DIR" ]] || fail "install directory is not writable: ${INSTALL_DIR}"

install -m 0755 "${unpack_dir}/lcroom" "${INSTALL_DIR}/lcroom"
install -m 0755 "${unpack_dir}/lcagent" "${INSTALL_DIR}/lcagent"

if [[ "$os_name" == "Darwin" ]]; then
  verify_macos_binary "${INSTALL_DIR}/lcroom"
  verify_macos_binary "${INSTALL_DIR}/lcagent"
fi

say "Installed lcroom and lcagent to ${INSTALL_DIR}"
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *) say "Add ${INSTALL_DIR} to PATH to run lcroom from any directory." ;;
esac
