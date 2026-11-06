# Changelog

## v0.1.0-alpha.8 — 2026-11-06

- `ggrun-client` with no arguments opens a menu: servers with live status,
  new session, resume here or a recent project, add server, update token,
  main-model review setting; returns to the menu when Claude exits.
  `ggrun-client start` is the prompt-based equivalent.
- Any Claude Code 2.1.307+ runs (2.1.316+ for main-model review and /ggoal);
  versions newer than tested are marked, not refused. Live-tested on 2.1.319.

## v0.1.0-alpha.7 — 2026-11-06

- Goal checks: one failed check keeps the goal active and continues work; it
  pauses only after 3 failures in a row (#6). A goal lock left by a killed
  hook is reclaimed after 30 s (#7).
- Reviews retry once after a lost connection and report `max_tokens`
  distinctly (#8).
- Includes alpha.6 (its tag was not published: GitHub retired the macos-14
  runner; CI now uses macos-15).

## v0.1.0-alpha.6 — 2026-11-06

- Loopback relay with HTTP/2 PING and TCP keepalive: a dead server path fails
  in about a minute and Claude retries, instead of hanging (#2, #3). Main-model
  reviews use the same transport.
- Status line shows the server's prefill/decode speed and queue, and warns when
  a request is lost, a stream stalls, the connection drops, or Claude Code is
  untested or changed on disk (#3, #4). Auto-updater disabled for every session.

## v0.1.0-alpha.2 — 2026-10-28

- Reject saved empty connection overrides and case variants on Windows before
  Claude can clear the verified gateway URL, credential or model/context binding.
- Reject raw resume/continue aliases, compact short options containing them, and
  session forks that bypass the connector verified-session metadata.

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
