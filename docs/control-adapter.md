# Experimental main-model control adapter

This is engineering work in progress. It is not installed by the default Claude
launcher and has not replaced native `/goal`. Qualification currently uses
Claude Code 2.1.316 on Linux. Cross-compilation is not native platform evidence.

Native Auto reviews have a separate input-dependent deadline (60 seconds in the
reported failure); native `/goal` uses a prompt Stop hook with a 30-second
default. Raising the general API timeout does not override these timers.

`experimental-review-hook --name CONNECTION --timeout 10m` reads a native
PreToolUse event on stdin and evaluates its exact action using the saved
connection's main alias. It requires the native transcript, includes it without
silent truncation, and rejects missing, malformed, oversized or incomplete
evidence. A short wait handles native transcript persistence lag. It emits a
permission decision only after a complete valid model verdict. This replaces a
review decision path and therefore needs decision-quality qualification, beyond
proving that a longer wait works.

For isolated qualification, a native command hook uses this executable and
subcommand, an outer timeout longer than the inner budget, and
`onFailure: "block"`. Test every existing hook together, including input
rewriters: a decision must cover the action actually executed. Never install
this by overwriting the user's settings or by broadening permission allow rules.
Native deny/ask rules and sandbox boundaries must remain effective.

The command uses the authenticated main endpoint, refuses credential redirects,
checks connection identity/context, and shares one context deadline with model
evaluation. It does not launch a second model. The request disables thinking both
through Anthropic metadata and llama.cpp template kwargs: some bundled backends
ignore `thinking.type=disabled` in Anthropic translation. Only control requests
receive this setting. Unsupported backends must fail visibly rather than issue
an approval. Reasoning blocks are never interpreted as final verdicts.
The adapter requests a data-only `submit_review` formatter tool to constrain
the verdict. Commentary accompanying a formatter call is not authoritative.
Exactly one named formatter result is required for a function-call reply;
unexpected tools and multiple verdicts are rejected.

A native configurable prompt Stop hook can test the same evaluator with a longer
timeout. That does not alter built-in `/goal` registration. A product replacement
still needs explicit goal lifecycle, interruption, error, clear/resume and
compaction behavior before it can replace the existing goal workflow.
The native evaluator's JSON schema must also be honored: some backend Anthropic
translations silently drop `output_config.format`. A controlled test adapter
can enforce the same schema via a formatter tool, but this protocol bridge has
not yet been implemented in the production goal path.

Required remaining evidence includes cold long-context review, genuine denied
actions and prompt injection, existing hook composition, child actions,
background work, goal continuation against independent oracles, resume and
compaction, plus supported platform runtime checks. A real native invocation
whose foreground actions are driven by a fixture provider remains a controlled
integration test, not proof of an autonomous production coding workload.
