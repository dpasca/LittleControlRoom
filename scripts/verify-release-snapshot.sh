#!/usr/bin/env bash

set -euo pipefail

dist_dir="${1:-dist}"
expected_archives=(
  "lcroom_Darwin_arm64.zip"
  "lcroom_Darwin_x86_64.zip"
  "lcroom_Linux_arm64.tar.gz"
  "lcroom_Linux_x86_64.tar.gz"
)

if [[ ! -d "${dist_dir}" ]]; then
  echo "release verification failed: ${dist_dir} does not exist" >&2
  exit 1
fi

for required_file in checksums.txt artifacts.json; do
  if [[ ! -f "${dist_dir}/${required_file}" ]]; then
    echo "release verification failed: missing ${dist_dir}/${required_file}" >&2
    exit 1
  fi
done

for archive in "${expected_archives[@]}"; do
  if [[ ! -f "${dist_dir}/${archive}" ]]; then
    echo "release verification failed: missing ${dist_dir}/${archive}" >&2
    exit 1
  fi
done

expected_checksum_names="$(
  printf '%s\n' "${expected_archives[@]}" | LC_ALL=C sort
)"
actual_checksum_names="$(
  awk '{
    name = $2
    sub(/^\*/, "", name)
    print name
  }' "${dist_dir}/checksums.txt" | LC_ALL=C sort
)"
if [[ "${actual_checksum_names}" != "${expected_checksum_names}" ]]; then
  echo "release verification failed: checksums.txt does not list exactly the expected archives" >&2
  diff -u <(printf '%s\n' "${expected_checksum_names}") <(printf '%s\n' "${actual_checksum_names}") || true
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  (
    cd "${dist_dir}"
    sha256sum --check checksums.txt
  )
elif command -v shasum >/dev/null 2>&1; then
  (
    cd "${dist_dir}"
    shasum -a 256 -c checksums.txt
  )
else
  echo "release verification failed: sha256sum or shasum is required" >&2
  exit 1
fi

for required_command in tar unzip; do
  if ! command -v "${required_command}" >/dev/null 2>&1; then
    echo "release verification failed: ${required_command} is required" >&2
    exit 1
  fi
done

expected_entries="$(
  printf '%s\n' LICENSE README.md lcagent lcroom | LC_ALL=C sort
)"

archive_entries() {
  local archive_path="$1"
  case "${archive_path}" in
    *.tar.gz)
      tar -tzf "${archive_path}"
      ;;
    *.zip)
      unzip -Z1 "${archive_path}"
      ;;
    *)
      echo "release verification failed: unsupported archive ${archive_path}" >&2
      return 1
      ;;
  esac | awk '{
    sub(/^\.\//, "")
    if ($0 !~ /\/$/) {
      print
    }
  }'
}

for archive in "${expected_archives[@]}"; do
  archive_path="${dist_dir}/${archive}"
  actual_entries="$(
    archive_entries "${archive_path}" | LC_ALL=C sort
  )"
  if [[ "${actual_entries}" != "${expected_entries}" ]]; then
    echo "release verification failed: unexpected contents in ${archive}" >&2
    diff -u <(printf '%s\n' "${expected_entries}") <(printf '%s\n' "${actual_entries}") || true
    exit 1
  fi
  echo "${archive}: contents OK"
done

echo "release snapshot verified"
