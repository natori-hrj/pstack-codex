# Harness support and adapters

[日本語](adapters.ja.md)

The shared workflow is the Markdown under `skills/`: acceptance, protected actions, task ownership, review budget and candidate evidence. Workflow Support provides these assets and host guidance; Native Automation requires a verified adapter. Project instructions and the effective permission policy govern each action.

| Operation | Adapter contract | Codex | OpenCode Workflow Support |
|---|---|---|---|
| Discovery | Install and invoke the same workflow. Verify the resolved skill path. | Native skills and plugins. | Standard OpenCode v2 skill discovery. |
| Roles | Resolve the role, model, effort and effective access before dispatch. Report a missing route. | Custom agent TOMLs and active model catalog. | User-configured session roles and variants. |
| Isolation | Require an enforced read-only reviewer and separate writable scopes for concurrent writers. | Native sandbox plus separate sessions and worktrees. | Serial workflow; operator verifies independent read-only access. |
| Lifecycle | Own automated work and verify terminal state. Keep session, worktree and revision evidence. | Go adapter and native app-server. | Operator-managed sessions. |
| Delivery | Use authorized Git and PR operations; bind evidence to the exact candidate and compare remote head. | Go workflow with explicit delivery authorization. | Operator-managed; no native verdict schema gate or automation. |

If parallelism is optional and the serial route satisfies all required permissions and capabilities, run serially when writer isolation is unavailable. If a mandatory capability cannot be verified, stop the affected work, keep the verified evidence and report the blocker. See the [Codex adapter](codex.md), the [OpenCode guide](opencode.md), and the [v0.3.0 behavior baseline](behavior-v0.3.0.md). Claude Code, Grok Build and other harnesses are unsupported.

For each delivery candidate record the base SHA, candidate SHA, command and result for each gate, reviewer and reviewed SHA, and known limitations. Compare the PR head to the verified local SHA before calling it ready. A changed head invalidates evidence for affected behavior. Repository and host rules determine who may commit, push a feature branch, open a PR or perform a gated operation.

The Go `HarnessAdapter` and `GateRunner` define preflight, gate and turn operations. An adapter must fail when it cannot establish the selected model and permissions, isolate each review context, enforce read-only review, or return correlated terminal evidence and the native schema-constrained result. The Go Core controls Git, candidate binding, review budgets and authorized delivery. The Codex adapter uses the native app-server protocol. The [v0.3.0 behavior record](behavior-v0.3.0.md) and [v0.4.0 comparison suite](https://github.com/natori-hrj/nstack/tree/v0.4.0/tests) preserve the basis for the Go contract tests.
