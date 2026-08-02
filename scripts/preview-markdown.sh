#!/usr/bin/env bash

# Render a Markdown file to a self-contained HTML preview and open it.
# Images are embedded, so the output works from anywhere and needs no server.

set -euo pipefail

source_doc="${1:-README.md}"
output_html="${2:-/tmp/lcroom-markdown-preview.html}"

if [[ ! -f "${source_doc}" ]]; then
  echo "markdown preview failed: ${source_doc} does not exist" >&2
  exit 1
fi

if ! command -v pandoc >/dev/null 2>&1; then
  echo "markdown preview requires pandoc (macOS: brew install pandoc, Debian/Ubuntu: apt install pandoc)." >&2
  exit 1
fi

style_file="$(mktemp -t lcroom-preview-style)"
trap 'rm -f "${style_file}"' EXIT

cat >"${style_file}" <<'CSS'
<style>
  body { max-width: 900px; margin: 40px auto; padding: 0 24px;
         font: 16px/1.6 -apple-system, "Segoe UI", Helvetica, Arial, sans-serif;
         color: #1f2328; background: #fff; }
  h1, h2 { border-bottom: 1px solid #d1d9e0; padding-bottom: .3em; margin-top: 1.6em; }
  h3 { margin-top: 1.6em; }
  code { background: #eff1f3; padding: .2em .4em; border-radius: 6px; font-size: 85%; }
  pre { background: #f6f8fa; padding: 16px; border-radius: 6px; overflow: auto; }
  pre code { background: none; padding: 0; }
  blockquote { border-left: .25em solid #d1d9e0; padding: 0 1em; color: #59636e; margin-left: 0; }
  table { border-collapse: collapse; display: block; overflow: auto; }
  th, td { border: 1px solid #d1d9e0; padding: 6px 13px; }
  tr:nth-child(2n) { background: #f6f8fa; }
  img { max-width: 100%; }
  a { color: #0969da; text-decoration: none; }
  details { margin: 1em 0; }
  summary { cursor: pointer; font-weight: 600; }
  @media (prefers-color-scheme: dark) {
    body { background: #0d1117; color: #e6edf3; }
    code { background: #2a2f37; }
    pre { background: #161b22; }
    th, td { border-color: #3d444d; }
    tr:nth-child(2n) { background: #161b22; }
    h1, h2 { border-color: #3d444d; }
    a { color: #4493f8; }
    blockquote { border-color: #3d444d; color: #9198a1; }
  }
</style>
CSS

# Relative image paths resolve against the source document's directory.
pandoc "${source_doc}" \
  --from gfm \
  --to html5 \
  --standalone \
  --embed-resources \
  --resource-path "$(dirname "${source_doc}")" \
  --metadata title="${source_doc} preview" \
  --include-in-header "${style_file}" \
  --output "${output_html}"

echo "rendered ${source_doc} -> ${output_html}"

if [[ -n "${LCROOM_PREVIEW_NO_OPEN:-}" ]]; then
  exit 0
fi

if command -v open >/dev/null 2>&1; then
  open "${output_html}"
elif command -v xdg-open >/dev/null 2>&1; then
  xdg-open "${output_html}"
else
  echo "no opener found; open ${output_html} manually" >&2
fi
