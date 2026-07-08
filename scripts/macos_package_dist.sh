#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS package signing/notarization must run on macOS" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"
PACKAGE_WORK_DIR="${PACKAGE_WORK_DIR:-${DIST_DIR}/macos-pkg}"

APP_SIGN_ID="${MACOS_APP_SIGN_ID:-Developer ID Application: NEWTYPE K.K. (69NH26W767)}"
INSTALLER_SIGN_ID="${MACOS_INSTALLER_SIGN_ID:-Developer ID Installer: NEWTYPE K.K. (69NH26W767)}"
NOTARY_TEAM_ID="${MACOS_NOTARY_TEAM_ID:-69NH26W767}"
NOTARYTOOL="${NOTARYTOOL:-xcrun notarytool}"
STAPLER="${STAPLER:-xcrun stapler}"

if [[ -z "${MACOS_NOTARY_APPLE_ID:-}" ]]; then
  MACOS_NOTARY_APPLE_ID="$(pass show common/remote/apple/dev_username)"
fi
if [[ -z "${MACOS_NOTARY_PASSWORD:-}" ]]; then
  MACOS_NOTARY_PASSWORD="$(pass show common/remote/apple/dev_secret)"
fi

metadata_file="${DIST_DIR}/metadata.json"
if [[ -n "${VERSION:-}" ]]; then
  package_version="${VERSION}"
elif [[ -f "${metadata_file}" ]]; then
  package_version="$(sed -nE 's/.*"version":"([^"]+)".*/\1/p' "${metadata_file}")"
else
  package_version=""
fi
if [[ -z "${package_version}" ]]; then
  echo "could not determine package version; set VERSION or build dist/metadata.json first" >&2
  exit 1
fi

package_id="${MACOS_PACKAGE_ID:-com.littlecontrolroom.lcroom}"

package_one_arch() {
  local arch="$1"
  local target_suffix="$2"
  local asset_arch="$3"
  local lcroom_bin="${DIST_DIR}/lcroom_darwin_${target_suffix}/lcroom"
  local lcagent_bin="${DIST_DIR}/lcagent_darwin_${target_suffix}/lcagent"
  local root="${PACKAGE_WORK_DIR}/${arch}/root"
  local pkg="${DIST_DIR}/lcroom_Darwin_${asset_arch}.pkg"

  if [[ ! -x "${lcroom_bin}" || ! -x "${lcagent_bin}" ]]; then
    echo "missing GoReleaser Darwin binaries for ${arch}; run make release-snapshot first" >&2
    exit 1
  fi

  rm -rf "${PACKAGE_WORK_DIR:?}/${arch}"
  mkdir -p "${root}/usr/local/bin"
  cp "${lcroom_bin}" "${root}/usr/local/bin/lcroom"
  cp "${lcagent_bin}" "${root}/usr/local/bin/lcagent"
  chmod 755 "${root}/usr/local/bin/lcroom" "${root}/usr/local/bin/lcagent"

  echo "signing ${arch} binaries"
  codesign --timestamp --force --verify --verbose \
    --sign "${APP_SIGN_ID}" \
    --options runtime \
    "${root}/usr/local/bin/lcroom" \
    "${root}/usr/local/bin/lcagent"

  echo "building ${pkg}"
  rm -f "${pkg}"
  pkgbuild \
    --root "${root}" \
    --identifier "${package_id}" \
    --version "${package_version}" \
    --install-location "/" \
    --sign "${INSTALLER_SIGN_ID}" \
    "${pkg}"

  echo "submitting ${pkg} for notarization"
  ${NOTARYTOOL} submit "${pkg}" \
    --apple-id "${MACOS_NOTARY_APPLE_ID}" \
    --password "${MACOS_NOTARY_PASSWORD}" \
    --team-id "${NOTARY_TEAM_ID}" \
    --wait

  echo "stapling ${pkg}"
  ${STAPLER} staple "${pkg}"
  ${STAPLER} validate "${pkg}"
  spctl -a -vv -t install "${pkg}"
  pkgutil --check-signature "${pkg}"
}

package_one_arch "arm64" "arm64_v8.0" "arm64"
package_one_arch "x86_64" "amd64_v1" "x86_64"

(
  cd "${DIST_DIR}"
  shasum -a 256 lcroom_Darwin_arm64.pkg lcroom_Darwin_x86_64.pkg > checksums-macos-pkg.txt
)

echo "created signed and notarized macOS packages:"
echo "  ${DIST_DIR}/lcroom_Darwin_arm64.pkg"
echo "  ${DIST_DIR}/lcroom_Darwin_x86_64.pkg"
echo "  ${DIST_DIR}/checksums-macos-pkg.txt"
