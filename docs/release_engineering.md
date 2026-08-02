# Release Engineering

Maintainer notes for cutting a Little Control Room release. Regular users do not
need any of this.

## Signing and notarization

macOS release binaries are signed and notarized by the release workflow. A tagged
release must have the Apple Developer credentials configured; otherwise the
release fails instead of publishing unsigned macOS artifacts. The installer
verifies published signatures locally; notarization acceptance is enforced during
the GitHub release job.

Required GitHub secrets:

| Secret | Value |
| --- | --- |
| `MACOS_SIGN_P12` | base64 contents of a Developer ID Application `.p12` certificate, or a path when running GoReleaser locally |
| `MACOS_SIGN_PASSWORD` | password for the `.p12` |
| `MACOS_NOTARY_KEY` | base64 contents of the App Store Connect API `.p8` key, or a path when running locally |
| `MACOS_NOTARY_KEY_ID` | App Store Connect API key ID |
| `MACOS_NOTARY_ISSUER_ID` | App Store Connect issuer UUID |

## Archive contract

GoReleaser marks official archives with `distribution=github`; that build metadata
is what enables the in-app updater. Future package-manager builds should set their
own distribution value so update ownership stays with the package manager.

Release archives must keep containing both `lcroom` and `lcagent`,
`checksums.txt`, and GitHub-provided SHA-256 asset digests, because the updater
refuses incomplete or unverifiable releases.

## CI

Every push to `master` and every pull request runs `make build-check` on both
Linux and macOS, followed by the same cross-platform release snapshot build used
locally. GoReleaser is pinned in `.tool-versions`; CI installs that exact version
automatically.

## Before tagging

```bash
make release-check
make release-snapshot
```

`make release-snapshot` builds all four platform archives, verifies their
checksums, and confirms that every archive contains `lcroom`, `lcagent`,
`README.md`, and `LICENSE`. Snapshot archives under `dist/` are for local
verification only, not public distribution.

## In-app updater

Official GitHub release builds check for a newer stable release when the TUI
starts, at most once every 24 hours. A new version appears as bright
`/update <version>` text in the top bar.

The updater downloads nothing until the user highlights `Update & restart` and
confirms. It then verifies GitHub's SHA-256 asset digests and the published
checksum file, verifies Apple Developer signatures on macOS, replaces `lcroom`
and `lcagent` together with rollback protection, saves active engineer turns, and
restarts the TUI on the new binary.

Source and development builds never contact GitHub for updates. Build metadata
also keeps the updater out of package-manager-owned installations once those
distributions exist. `LCR_DISABLE_UPDATE_CHECKS=true` disables the once-daily
automatic check in an official build; `/update` still works as a manual check.
