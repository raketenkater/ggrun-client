# ggrun-client

Run Claude Code in your laptop's project while
[ggrun](https://github.com/raketenkater/ggrun) serves the local model on another
computer. The server owns inference and scheduling. Your laptop owns files,
tools, MCPs, memories and sessions. The connector launches your installed Claude
CLI; it does not replace the coding harness or include Claude binaries.

**Development status:** protocol v1 implementation, not yet released or qualified
on a real laptop. The initial Claude CLI target is **2.1.307**. Linux and Windows
contract tests and portable builds are separate from real GPU/laptop acceptance.
macOS is cross-built only until live evidence is available.

## Install

After the first release is published, install a checksummed binary without Go:

```sh
curl -fsSL https://raw.githubusercontent.com/raketenkater/ggrun-client/main/install.sh | sh
```

On Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/raketenkater/ggrun-client/main/install.ps1 | iex
```

Install [Claude Code](https://code.claude.com/docs/en/setup) separately. The
server requires a matching remote-enabled ggrun build. See the upstream
[server setup](https://github.com/raketenkater/ggrun/blob/main/docs/remote-claude.md).

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
locally. Auto permission mode follows ggrun's default; use an explicit Claude
`--permission-mode` or `GGRUN_CLAUDE_PERMISSION_MODE=inherit` to retain another
choice.

Client exit and disconnection leave the server model loaded. Interrupted tool
generations are not automatically replayed. Resume checks model/backend identity
and refuses reduced capacity. A server restart may lose prompt cache and need a
new prefill. The connector never stops or remotely controls the model process.

## Compatibility and development

| Contract | Target | Qualification |
|---|---|---|
| ggrun connection / policy | 1 / 1 | shared typed package and fixtures |
| Claude CLI | 2.1.307 | initial target; live qualification pending |
| Linux client | amd64, arm64 | native tests on amd64; portable builds |
| Windows client | amd64, arm64 | native CI required on amd64; portable builds |
| macOS client | arm64, amd64 | cross-build only |

Other Claude versions require explicit `--allow-untested-claude` on the `claude`
command. This permits a manual compatibility test and is not a compatibility
claim. Desktop, cloud sessions, Remote Control and independently supervised
background processes are outside the first milestone. Exact token counting is
not advertised by protocol v1's initial server implementation.

The public contract is maintained in ggrun's
[clientkit](https://github.com/raketenkater/ggrun/tree/main/clientkit).
The client must pin a reviewed package version/commit before publication; the
local development `replace` must be removed before release. Run `go test -race
./...`, `go vet ./...` and `go test github.com/raketenkater/ggrun/clientkit`.
CI exercises the actual child launch with a native fixture executable and no
Tailscale or model download. It does not substitute for a real laptop coding task.
