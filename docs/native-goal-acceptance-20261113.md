# Native goal reliability: implementation and acceptance record

## Result

Native `/goal` acceptance FAILED. Do not release or describe this branch as a
complete solution for unattended local-model goals. The independent client
changes are prepared for review, not deployed.

## Live evidence

On 2026-11-13, a disposable project ran installed Claude Code 2.1.323 interactively
against the existing main backend on loopback. The actual executable resolves to
`.src/fork-qwen3-8-flash-next/build-cuda/bin/llama-server`. The gateway advertises
server v3.2.13-32-gea69bf1, 144384 tokens/slot, one slot, and a separate worker.
Direct-main tests deliberately bypassed helper routing; this does not qualify
the ordinary gateway/TUI path. No backend restart or placement change occurred.

The native goal condition asked for a response containing READY. Claude answered,
then printed `Goal paused · the goal check timed out`. The test observed that
message about 58 seconds after goal activation, including the foreground turn.
`API_TIMEOUT_MS=900000` did not prevent it. Read-only inspection of 2.1.323 also
confirmed the evaluator's 30000ms default and found no goal-timeout override.
This is direct evidence of an independent evaluator deadline, not a general
network timeout or an impossible-condition verdict.

A separate interactive comparison configured Anthropic's own prompt Stop hook
with `timeout: 600` and model `local`. It was stopped after the 180-second test
budget without a verified verdict. The foreground response itself was slow and
did not reliably follow the tiny test instruction, so this is NOT evidence that
the evaluator successfully ran longer than 30 seconds. No fallback was enabled
in the product.

A synthetic direct-main Anthropic request with disabled thinking, explicit
template kwargs, and `output_config.format.type=json_schema` returned HTTP 200
after 70.11 seconds, 24 output tokens, end_turn, but the final text was not a
parseable object satisfying even the requested `ok` boolean. This check was
queued alongside the comparison and is not an isolated latency benchmark.
The active fork's `tools/server/server-chat.cpp` passes template kwargs but
does not translate `output_config`, and only handles thinking.type=enabled.
The same omissions exist in the inspected GLM fork. These are concrete adapter
gaps, not proof that every backend or model fails. The current custom main-model
review already sends template kwargs and uses a forced formatter tool, so it
does not have exactly the same request format as the native goal evaluator.

## Independent changes

- Local commit cbd1a06 returns goal ownership to native Claude and removes
  automatic registration of the old custom goal hooks/plugin.
- Relay observes bounded incremental Anthropic SSE events without modifying
  streamed bytes. Heartbeats and empty deltas do not count as model progress.
- Generation idle policy defaults to 3 minutes and can be overridden/disabled.
  Startup silence stays unbounded unless explicitly configured because this
  gateway supplies aggregate, not request-correlated, prefill evidence.
- Expiry cancels upstream context. Canceled, failed, HTTP-error and truncated
  streams are not counted as successful completions. A truncated stream becomes
  an actual read error. No relay request replay was introduced.
- Status displays missing semantic progress and unknown model phase honestly.
- Saved per-connection customizations work through CLI/TUI import and ordinary
  new/resumed launches. Existing user hooks remain preserved.

## Timeout ownership

| Layer | Effective policy in this branch | Scope |
| --- | --- | --- |
| Dial / TLS handshake | 15s each | Establishing a connection |
| HTTP/2 liveness | Ping after 30s idle; 15s ping response budget | Peer/path responsiveness, not inference |
| TCP keepalive | Idle 15s, interval 10s, count 4 | Dead TCP paths |
| Startup/unknown phase | Off; GGRUN_REQUEST_STARTUP_TIMEOUT override | Explicit queue/prefill policy, not wedge proof |
| Identified generation | 3m idle; GGRUN_GENERATION_IDLE_TIMEOUT override | Resets only on semantic content progress |
| General native API request | Server profile currently supplies 2147483647ms | Does not extend native hooks |
| Main-model review | CLI default 10m; TUI default 30m | Independent review HTTP request |
| Review command hook | Configured inner review budget +30s | Native command-hook wrapper |
| Native goal evaluation | Observed default 30s | No supported override established |
| Workflow policy hook | 5s | Patches tool input; model work uses separate policy |

## Verification and remaining gates

Client race tests and vet pass during implementation. Final command results are
recorded in the worker's delivery message. Added tests cover fragmented and
multiline SSE, text/thinking/tool-input progress, bounded parser memory,
heartbeat-only generation cancellation, slow prefill followed by steady output,
exact forwarding, truncated/API/HTTP errors, startup policy, configuration
validation, saved customization precedence and connection isolation.

These are real HTTP fault fixtures but simulated inference. Existing native CI
has Linux, macOS and Windows test/race/vet/build jobs. Those remote jobs have not
run for these local changes; local cross-builds are not native OS execution.

Still required before epic #17 can close:

1. Backend-native translation of structured verdict constraints and explicit
   thinking policy, with portable backend capability checks and tests. Implement
   this in the maintained backend patch/adapter path, not just the running fork's
   source directory. Verify explicit template kwargs precedence and unsupported
   schema behavior. No native prompt matching or fabricated verdicts.
2. Decide how to handle native evaluator checks that legitimately exceed 30s:
   upstream configurable timeout, or explicitly agreed alternative. A longer
   Anthropic prompt hook is a candidate requiring its own successful proof and
   lifecycle qualification; this branch does not silently enable it.
3. Live cancellation propagation through gateway/router to the model slot,
   native retry/pause classification, main-only Auto review, compaction/resume,
   TUI default launch, and sustained autonomous task acceptance.
4. Request-correlated backend phase counters or a measured opt-in startup
   policy for before-generation wedges. Ping-only traffic before an identified
   generation phase remains progress-unknown; it is not automatically killed.
5. Native CI across client platforms, versioned installation/update and a
   controlled production rollout. No GitHub issues have been closed.
