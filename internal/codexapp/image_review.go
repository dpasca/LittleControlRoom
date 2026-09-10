package codexapp

const imageReviewTurnContext = `The operator explicitly enabled external image review for this session as recovery from native image-input failures.
- Keep this conversation text-only: do not call view_image or emit image content from tools while recovery is enabled.
- When a visual check is necessary for the current task, call lcr_runtime/inspect_images with workspace image paths, a focused question, and any diagnostic legends. The separate API reviewer returns text findings only.
- Use code, logs, and existing findings when sufficient. Do not repeatedly send unchanged images or expand into unrelated visual reviews.
- Start with one image to verify the external route works, then compare matching pairs when needed. If it fails, report the error and leave visual verification pending; do not retry native image loading.
- Attribute findings to the external reviewer and retain uncertainty. A successful tool call is not proof that the images passed review.
- This is a temporary session-specific recovery option, not a default workflow for other sessions.`
