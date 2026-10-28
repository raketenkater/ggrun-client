# Changelog

## Unreleased

- Native Apple Silicon macOS contract tests and published alpha installer checks,
  alongside Linux and Windows. Intel Mac remains cross-built only.
- Every release now verifies installation of its published binaries on all three
  client operating systems. Real laptop terminal acceptance remains pending.

## v0.1.0-alpha.1 — 2026-10-27

- Connection protocol v1 and policy v1: private profiles, generic HTTP(S), doctor,
  real Claude CLI launch, local Workflow helper and cached queue status.
- Bound resume to the last verified model/backend and context in each project.
- Linux and Windows native contract CI; six portable release binaries and
  checksummed installers. Published as an alpha for real laptop qualification.
- Initial qualified Claude CLI version: 2.1.307. The matching server must include
  its worker-report compatibility fix; earlier server drafts can omit handbacks.
