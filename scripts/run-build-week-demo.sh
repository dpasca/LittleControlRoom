#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if [[ -z "${OPENAI_API_KEY:-}" ]]; then
  echo "OPENAI_API_KEY is required for the Build Week demo." >&2
  exit 1
fi
case "$OPENAI_API_KEY" in
  *$'\n'*|*$'\r'*|*'"'*|*'\'*)
    echo "OPENAI_API_KEY contains a character that cannot be safely written to temporary TOML." >&2
    exit 1
    ;;
esac

demo_include_paths="${LCROOM_BUILD_WEEK_INCLUDE_PATHS:-$repo_root}"
demo_root="/tmp/lcroom-build-week-demo"

cleanup() {
  rm -rf /tmp/lcroom-build-week-demo
}

if [[ -e "$demo_root" ]]; then
  echo "Demo sandbox already exists: $demo_root" >&2
  echo "Check that no demo is running, then remove that exact directory and retry." >&2
  exit 1
fi
mkdir -m 700 "$demo_root"
trap cleanup EXIT INT TERM

demo_dir="$(mktemp -d "$demo_root/run.XXXXXX")"
demo_config="$demo_dir/config.toml"
demo_db="$demo_dir/little-control-room.sqlite"

umask 077
printf '%s\n' \
  "openai_api_key = \"$OPENAI_API_KEY\"" \
  'ai_backend = "openai_api"' \
  'boss_chat_backend = "openai_api"' \
  'boss_helm_model = "gpt-5.6"' \
  'boss_utility_model = "gpt-5.6-luna"' \
  'project_reasoning_effort = "medium"' \
  'lcagent_provider = "openai"' \
  'embedded_lcagent_model = "gpt-5.6"' \
  'embedded_lcagent_reasoning_effort = "low"' \
  'lcagent_utility_provider = "openai"' \
  'lcagent_utility_model = "gpt-5.6-luna"' \
  'mobile_enabled = false' \
  'mobile_input_enabled = false' \
  'hide_reasoning_sections = true' \
  >"$demo_config"

launcher=(go run ./cmd/lcroom)
if [[ -n "${LCROOM_BUILD_WEEK_BINARY:-}" ]]; then
  if [[ ! -x "$LCROOM_BUILD_WEEK_BINARY" ]]; then
    echo "LCROOM_BUILD_WEEK_BINARY is not executable: $LCROOM_BUILD_WEEK_BINARY" >&2
    exit 1
  fi
  launcher=("$LCROOM_BUILD_WEEK_BINARY")
fi

echo "Launching an isolated OpenAI Build Week demo profile"
echo "  Main agent:           GPT-5.6"
echo "  Background inference: GPT-5.6 Luna"
echo "  Included paths:       $demo_include_paths"
echo "  Personal config:      not loaded"

env \
  -u LCROOM_BOSS_MODEL \
  -u LCROOM_COMMIT_MODEL \
  -u LCROOM_SESSION_CLASSIFIER_MODEL \
  -u OPENAI_BASE_URL \
  -u OPENAI_FINAL_MODEL \
  -u OPENROUTER_API_KEY \
  -u OPENROUTER_BASE_URL \
  -u OPENROUTER_FINAL_MODEL \
  -u DEEPSEEK_API_KEY \
  -u DEEPSEEK_BASE_URL \
  -u DEEPSEEK_FINAL_MODEL \
  -u MOONSHOT_API_KEY \
  -u MOONSHOT_BASE_URL \
  -u MOONSHOT_FINAL_MODEL \
  -u XIAOMI_API_KEY \
  -u XIAOMI_BASE_URL \
  -u OLLAMA_API_KEY \
  -u OLLAMA_BASE_URL \
  -u OLLAMA_FINAL_MODEL \
  -u MLX_API_KEY \
  -u MLX_BASE_URL \
  "${launcher[@]}" tui \
  --config "$demo_config" \
  --db "$demo_db" \
  --include-paths "$demo_include_paths"
