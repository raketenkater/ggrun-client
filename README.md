# ggrun-client

### Request progress and saved customizations

The relay distinguishes received bytes from model output. Once an Anthropic
stream identifies generation, three minutes without text, thinking, or tool-input
progress cancels that request. SSE heartbeats do not reset this clock. Override
with `GGRUN_GENERATION_IDLE_TIMEOUT=10m`, or `0` to disable. This is an idle
budget, not a maximum generation duration.

Before generation, the relay reports progress as unknown. It cannot infer which
request is progressing from aggregate server status. Queueing and cold prefill
therefore have no default deadline; an optional
`GGRUN_REQUEST_STARTUP_TIMEOUT=30m` applies an explicit policy limit. These
variables must be set in the environment that starts ggrun-client. The relay
cancels upstream work but does not replay requests. Native Claude retry or pause
behavior still needs qualification for your workflow. A native `/goal` check has
its own deadline; neither relay setting extends it.

Import per-connection launch choices from a JSON file through the TUI's
“Import customizations” action, or:

```sh
ggrun-client customize --name default --file launch.json
```

Supported fields are `plugin_dirs` (array of absolute directory paths),
`mcp_config` (absolute path to an existing MCP file), `append_system_prompt`, and
`permission_mode` (`auto`, `default`, `acceptEdits`, or `plan`). Use `{}` to clear
saved choices. MCP credentials stay in the referenced file. Saved choices apply
to both new sessions and resume; explicit Claude flags override saved MCP,
prompt, and permission values. Explicit plugin directories extend the saved
list. If main-model controls are enabled, the effective permission mode must
remain `auto`; disable controls in the TUI when choosing another mode.

This branch's native-goal migration and progress handling are under validation.
The native evaluator timeout remains a known acceptance blocker; source tests
are not evidence of a released, fully autonomous workflow.

Run Claude Code in your laptop's project while
[ggrun](https://github.com/raketenkater/ggrun) serves the local model on another
computer. The server owns inference and scheduling. Your laptop owns files,
tools, MCPs, memories and sessions. The connector launches your installed Claude
CLI; it does not replace the coding harness or include Claude binaries.

**Development status:** protocol v1 alpha, [v0.1.0-alpha.2](https://github.com/raketenkater/ggrun-client/releases/tag/v0.1.0-alpha.2). Not yet qualified on a real laptop. The initial Claude CLI target is **2.1.307**. Linux and Windows
contract tests and portable builds are separate from real GPU/laptop acceptance.
Clients target macOS, Linux and Windows; headless servers target Linux and
macOS. Native macOS ARM64 tests and published-alpha installation passed; real-device
Claude terminal acceptance remains separate on every client OS.

## Install

Install the checksum-verified alpha binary without Go:

```sh
curl -fsSL https://raw.githubusercontent.com/raketenkater/ggrun-client/v0.1.0-alpha.2/install.sh | GGRUN_CLIENT_VERSION=v0.1.0-alpha.2 sh
```

On Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/raketenkater/ggrun-client/v0.1.0-alpha.2/install.ps1 -OutFile install-ggrun-client.ps1
./install-ggrun-client.ps1 -Version v0.1.0-alpha.2
```

Install [Claude Code](https://code.claude.com/docs/en/setup) separately. The
server requires a matching remote-enabled ggrun build. See the upstream
[server setup](https://github.com/raketenkater/ggrun/blob/feat/remote-claude-client/docs/remote-claude.md).

## Use

Choose **Remote client** in the server's ggrun TUI and launch the model. On the
laptop, run `ggrun-client` with no arguments in your project directory:

```sh
cd your-project
ggrun-client
```

It opens a menu: add a server (URL + gateway token; on the server run
`ggrun gateway-token`), see whether it is ready or still loading, start a new
session here, resume the last one here or a recent project elsewhere, update a
changed token, and toggle main-model review. When Claude exits you return to
the menu. Inside the session, use Anthropic’s native `/goal <condition>` for long-running goals.
`ggrun-client start` asks the same questions as plain prompts.

Claude Code 2.1.307 or newer works; main-model review needs 2.1.316
or newer. Tested: 2.1.316 and 2.1.319. Newer releases run, marked untested.

The individual commands remain available:

```sh
ggrun-client connect https://models.example.com --name server
ggrun-client doctor --name server
cd your-project
ggrun-client claude --connection server
ggrun-client claude --connection server --resume
```

Pass ordinary Claude options after `--`, for example
`ggrun-client claude --connection server -- -p "Fix the failing tests"`.
Model/session overrides must use the connector's verified connection and resume
options. Resume currently supports the last connector-managed session for that
connection and project directory.

Any supported HTTP(S) URL works: HTTPS proxy, LAN, local SSH tunnel or optional
VPN. Tailscale is one optional deployment and is not required or configured by
this program. Reverse-proxy path prefixes and IPv6 URLs are supported. Do not
append `/v1`. Remote HTTP requires deliberate `--allow-http`; loopback HTTP is
accepted for local tunnels. Diagnostics never redirect credentials to another URL.

Profiles live outside project repositories in your OS user configuration
directory. Unix files use mode 0600; Windows uses user-bound DPAPI encryption.
Tokens are accepted through the hidden prompt, `GGRUN_AUTH_TOKEN` or
`connect URL --token-stdin`. Tokens are never command-line arguments. Replace a
connection using `forget --name server`, then reconnect; this preserves Claude's
own sessions and settings.

The launcher supplies the actual context per slot, compaction, aliases, stable
prompt attribution and local-inference timeout policy. It preserves user hooks,
MCPs, permission choices and custom status lines. Conflicting saved connection
settings are diagnosed. With explicit custom `--settings`, configure the
`ggrun-client workflow-hook` PreToolUse hook for Workflow yourself. Web research
can use your search MCP or the optional DuckDuckGo MCP when `uvx` is installed
locally. Permission mode defaults to Auto. Main-model safety checks can exceed
Claude's separate classifier deadline, even with a long API timeout; autonomous
serving needs a reviewer that completes these checks within that deadline.
Use an explicit Claude `--permission-mode` or
`GGRUN_CLAUDE_PERMISSION_MODE=inherit` to retain another
choice.

If an existing session reports that Auto mode cannot determine a tool's safety
because the model timed out, exit and resume it with normal permission prompts:

```sh
ggrun-client claude --connection server --resume -- --permission-mode default
```

This preserves the conversation and normal tool approval rules. It does not
require a model restart or a separate reviewer. It also works with alpha.2.

Client exit and disconnection leave the server model loaded. Interrupted tool
generations are not automatically replayed. Resume checks model/backend identity
and refuses reduced capacity. A server restart may lose prompt cache and need a
new prefill. The connector never stops or remotely controls the model process.

## Compatibility and development

| Contract | Target | Qualification |
|---|---|---|
| ggrun connection / policy | 1 / 1 | shared typed package and fixtures |
| Claude CLI | 2.1.307 | same-host coding/resume/foreground worker passed; laptop pending |
| Linux client | amd64, arm64 | native tests on amd64; portable builds |
| Windows client | amd64, arm64 | native tests and released install passed; real terminal pending |
| macOS client | arm64, amd64 | portable builds; native ARM64 and alpha install passed; Intel cross-built only |

The server candidate must include the role-compatibility fix from ggrun
`b061f6b` or later: some templates silently omit mid-conversation system
messages carrying worker reports. The bridge rejects those roles so the
qualified Claude version retries with the report in user content. Earlier
feature drafts are not qualified for worker handoff.

Other Claude versions require explicit `--allow-untested-claude` on the `claude`
command. This permits a manual compatibility test and is not a compatibility
claim. Desktop, cloud sessions, Remote Control and independently supervised
background processes are outside the first milestone. Exact token counting is
not advertised by protocol v1's initial server implementation.

The public contract is maintained in ggrun's
[clientkit](https://github.com/raketenkater/ggrun/tree/feat/remote-claude-client/clientkit).
The client pins the exact server feature commit in go.mod. Local development
replacements are forbidden by the release gate. Run `go test -race
./...`, `go vet ./...` and `go test github.com/raketenkater/ggrun/clientkit`.
CI exercises the actual child launch with a native fixture executable and no
Tailscale or model download. It does not substitute for a real laptop coding task.
# Main-model controls (alpha.5)

Extra ggrun tool review is optional and independent of goal continuation.
It uses the main model and requires Claude Code 2.1.316 or newer. Native
permission rules still apply; this does not remove all native classifier
deadlines. New TUI and guided launches leave extra review off unless selected.
The old ambiguous `controls` setting no longer implicitly enables review;
re-enable it through the TUI if wanted.

```sh
claude install 2.1.316
ggrun-client connect https://YOUR-SERVER --name server
ggrun-client claude --connection server --tool-review --control-timeout 30m
```

Run the client in your working project. Tools, files, hooks and goal state stay
on that computer. The backend serves the model API only. Existing verified
sessions can add `--resume`. Explicit non-Auto modes require omitting
`--tool-review` (legacy alias `--local-controls`); changing mode inside Claude returns later permission
decisions to native policy (a review already in flight still applies its
verdict). Existing deny/ask rules remain effective.

For goals, use Anthropic's native controller inside Claude:

```text
/goal all tests pass and the README documents the new flag
/goal
/goal clear
```

For the explicitly selected client completion controller with a longer budget:

```sh
ggrun-client claude --connection server --goal-only --goal-file GOAL.md --control-timeout 30m
```

Then give Claude the task in that session. In the TUI, selecting a client goal
file also supplies an initial instruction to read it and begin. Goal selection
does not enable extra tool review. `--no-tool-review` explicitly disables the
extra reviewer even when the legacy `--local-controls` flag is supplied.
Native permissions, user hooks and deny/ask rules remain effective.

The client controller installs a Stop evaluator and a StopFailure notification
hook; it does not install `/ggoal` or a
UserPromptSubmit hook. Do not also activate native `/goal` in that session.
From the same project directory, `ggrun-client goal status`, `goal clear` and
`goal resume` manage the client condition. Resume requires the same verified
running session with the client hook; a new launch with `--goal-file` explicitly binds
that file to the selected new/resumed session. Native and client goal state are
separate. Client conditions are limited to 16384 bytes; native conditions to
4000 characters. Keep longer work briefs in a separate file.

Only one client launch may own a connection/project session at a time. Exiting
the client pauses an unfinished goal. `goal status` detects a crashed launcher
and marks stale active state as paused; `goal resume` cannot revive a dead
session. A native API failure records a pause through StopFailure. That hook
cannot start another turn: after resolving the error, resume the client goal
and send Claude a message. Automatic recovery of aborted native turns remains
unqualified.

Read-only evaluator calls retry transient transport/server failures and invalid
verdicts up to three attempts within the existing control timeout. Valid
allow/deny/ask decisions are final; authentication and invalid-request errors do
not retry. Repeated failed checks pause the goal, and invalid output never
becomes approval or completion. Tool calls themselves are never replayed.

To select custom control models independently, import a per-connection
customization file through the TUI or `ggrun-client customize --name server
--file customizations.json`, for example:

```json
{"goal_evaluator":"utility","tool_reviewer":"main"}
```

Both default to `main`; selecting a tool reviewer does not enable tool review.
Utility selection requires a server advertising `utility_route` capacity.
Evidence is checked against that route's conservative context, with no silent
truncation. The launch message shows the alias and whether the server may fall
back to main. These choices affect the custom client controls, not native
`/goal` or native Auto classification. The server's existing native classifier
policy can route requests independently of their requested alias.

Workflow concurrency is capped to the selected executor's advertised capacity;
a separate reviewer does not add main-model slots. A lower valid user limit is
preserved. This limits fan-out but does not prove that every native Workflow or
structured-output stage succeeds on the selected model.

The native evaluator in inspected Claude Code 2.1.323 still uses a 30-second
prompt-hook default. `--control-timeout` controls ggrun's optional reviewer and client goal evaluator,
**not that native timer**. General API timeouts and relay keepalives do not extend
it. Do not describe this migration as a fixed native timeout or promise that a
cold slow main-model check will finish. That requires separate live qualification
and an upstream timeout override or demonstrated latency improvement.

### Connection health and status line

Claude Code talks to a loopback relay inside `ggrun-client`, which forwards to
the server. The model may stay silent for as long as a prefill needs; the relay
instead checks the path itself (HTTP/2 PING, TCP keepalive). When the path is
dead (laptop sleep, network change, proxy restart) the request fails in about a
minute and Claude retries on a new connection instead of waiting indefinitely.

The status line shows the server's live progress (prefill/decode speed, queue)
as a local `ggrun claude` session does. Servers without the progress endpoint
fall back to queue counts. It also warns when a request is not reaching the
server, a stream stops sending data, the connection was lost, or the running
Claude Code version is untested or changed on disk. Claude's auto-updater is
disabled for the session so the qualified version stays in use.
