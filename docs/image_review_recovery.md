# External image review recovery

Use this only for a Codex session whose native image input is failing. It is an
explicit recovery option, not an automatic fallback or a default vision route.

In an idle embedded Codex pane:

```text
/image-review on
```

LCR checks local API credentials before replacing the helper, reconnects the exact
current thread, displays **External image review** in the pane banner, and exposes
`lcr_runtime/inspect_images` to that session. No review is submitted by enabling
the option. Ask Codex to perform the pending visual check after reconnecting.

The reviewer uses `gpt-5.6-luna` with `high` reasoning through the OpenAI Responses
API, using the existing LCAgent model adapter. It uses LCR's configured OpenAI API
key, or `OPENAI_API_KEY` from the environment; `OPENAI_BASE_URL` is honored for
configured API endpoints. Credentials pass through the helper environment, never
command-line arguments. This consumes separately billed API usage, not the
embedded Codex subscription allowance. Availability must be verified against the
configured API account; a failure is returned explicitly without switching models.

The tool takes:

```json
{
  "paths": ["build/captures/baseline.png", "build/captures/expanded.png"],
  "question": "Compare visible terrain seams and texture stretching.",
  "context": "Same altitude, camera position and yaw. Include the diagnostic color legend here."
}
```

Images are sent in the listed order as separate image inputs, with a fresh request
and no main-session history or tools. The main session receives only text findings,
image paths, the actual response model, and usage metadata. The request disables
API response storage (`store: false`); this is not a promise of zero provider data
retention. The reviewer is instructed to identify approximate defect locations,
separate observations from hypotheses, and state uncertainty. A successful API
call does not mean the visual result passed review.

Start with one image to check connectivity, then review matching pairs as needed.
Reuse existing findings for unchanged images. Code, logs, build results, and
performance checks remain separate evidence. Never infer performance or whole-task
acceptance from a screenshot review.

The tool accepts 1–4 PNG, JPEG, or GIF files, each at most 20 MiB and 16 megapixels.
Paths must resolve inside the session's workspace, including symlink resolution.
The API call has a two-minute deadline. Errors and empty responses return a failed
tool result without automatic retries. Only static images should be used; this
tool does not review motion or a capture sequence as video.

While enabled, Codex receives a turn instruction to keep its conversation
text-only and use external review only for necessary visual checks. Image
attachments submitted through LCR are rejected with a message to supply workspace
paths instead. This instruction does not rewrite images already in a broken
thread: if that thread still cannot make text-only requests, create a fresh
handoff with an instruction to avoid native image loading, then explicitly enable
recovery there.

```text
/image-review
/image-review off
```

The first command reports the state; the second reconnects with the tool removed.
Switches are ignored while a turn or another open operation is busy. Explicit
`/reconnect` preserves the setting for the current thread. New sessions, handoffs,
and sessions reopened after LCR restarts default to off; there is no persisted
project-wide or global preference. Ordinary sessions receive neither the tool nor
its recovery instruction. A caller guessing the disabled tool name is rejected.

Codex errors now retain `codexErrorInfo` and `additionalDetails` when supplied by
app-server, including in resumed transcript errors. This aids diagnosis of native
image failures without classifying errors by keywords or automatically enabling
external review.

## Validation

Unit tests cover opt-in discovery and rejection, exact-thread reconnect,
busy-session guards, missing credentials, image path validation, independent API
payloads, text-only results, and error propagation. The live smoke test is explicit
and uses generated solid-color images:

```sh
LCR_TEST_LIVE_IMAGE_REVIEW=1 go test ./internal/imagereview -run '^TestLiveImageReview$' -v
```

It sends one image, then a pair. Ordinary test runs never make these API calls.
