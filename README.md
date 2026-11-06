# ggrun-client

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
laptop, use its advertised HTTP(S) base URL and enter the gateway token at the
hidden prompt:

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

For a backend-only server and Claude Code on another computer, install alpha.5
on the **client computer**. This mode uses the main model for reviews, without a
second model or native Auto's short classifier deadline. Requires Claude Code
2.1.316. It remains an opt-in deployment mode; other native versions and all
model/hardware combinations are not qualified by this deployment's tests.

```sh
claude install 2.1.316
ggrun-client connect https://YOUR-SERVER --name server
ggrun-client claude --connection server --local-controls --control-timeout 30m
```

Run the client in your working project. Tools, files, hooks and goal state stay
on that computer. The backend serves the model API only. Existing verified
sessions can add `--resume`. Explicit non-Auto modes require omitting
`--local-controls`; changing mode inside Claude returns later permission
decisions to native policy (a review already in flight still applies its
verdict). Existing deny/ask rules remain effective.

For an autonomous goal, type this inside the session (it is the client's
replacement for native `/goal`, checked by the main model with no 30-second
deadline):

```text
/ggoal all tests pass and the README documents the new flag
/ggoal status
/ggoal clear
/ggoal resume
```

The goal binds to the conversation it was typed in; `/ggoal resume` moves it
to the current one (for example after `/clear`). Or write the condition to
`GOAL.md` and start with it:

```sh
ggrun-client claude --connection server --local-controls --control-timeout 30m --goal-file GOAL.md
```

Tell Claude to read GOAL.md and start. Both are the client's persistent goal
controller, **not native `/goal`**, whose short deadline is unchanged. From
another terminal in the same project:

```sh
ggrun-client goal status --connection server
ggrun-client goal clear --connection server
ggrun-client goal resume --connection server
ggrun-client goal set --connection server --file GOAL.md
```

An unmet verdict continues work; a complete verdict records completion. A
failed check continues work and checks again at the next stop; three failures
in a row pause the goal without claiming success. Clear/replacement
cancels an in-flight check. Resume preserves the condition and verified session.
Native Stop continuation caps still apply when the agent repeatedly makes no
tool progress; the saved goal is not marked complete when that cap stops a turn.
Long-context checks can take several minutes. Review quality still depends on
the selected model. Custom input-rewriting hooks and large workflows require
their own workload qualification; a simple live task is not proof of every
workflow. No user settings files are overwritten.

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
