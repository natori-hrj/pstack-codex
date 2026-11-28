# Codex adapter

[日本語](codex.ja.md)

Install the skills with the [Go binary](../README.md#install-from-the-single-binary). To set up custom roles, copy the necessary TOMLs from the staged snapshot's `examples/agents/` to `${CODEX_HOME:-$HOME/.codex}/agents/` only if the destination file and symlink are absent. Model and reasoning effort are user settings; edit existing role files only by choice.

| Upstream mechanism | Codex equivalent |
|---|---|
| Skills | Invoke `$name` explicitly. Workflows load the specific skills and playbooks they need. |
| Task delegation and model rules | Select an available native custom agent by name. Its TOML sets the model and reasoning effort. |
| Parallel writers | Assign one writer per task, branch, and Git worktree. Open each worktree in its own session with `codex -C <worktree>`. |
| Independent review | Use `nstack_review` from a read-only parent, or start a separate session with `codex exec -C <worktree> --sandbox read-only -m <model>`. |
| Structured output | Use `codex exec --output-schema <file>` when a task needs it. |
| Session recovery | Use `codex resume <session-id>`, or `codex exec resume <session-id>` for non-interactive sessions. |
| Cursor Cloud, Bugbot, Automations, and transcript integration | Outside this adapter’s scope. Use available CI and review evidence and report any gaps. |

## Permissions and access

Custom agents inherit the parent’s permissions. Set the actual sandbox before starting an independent review. A Git worktree provides a separate checkout. Check sandbox writable roots separately and keep sibling worktrees outside them. Serialize tasks that modify the same files.

Before delivery, pin the candidate revision and run the mandatory quality gates. Refresh affected checks after changes. Routine commits, feature-branch pushes and PRs follow repository authorization. Release publication, merges and destructive cleanup require explicit approval. Inspect imported configuration, hooks, commands, and agent output before acting on them.

The [role examples](../examples/agents/) set Sol for implementation and authorized defensive work, and Astra for independent review. If a mandatory route is unavailable, stop that task. Daybreak requires an approved account and model. With approved access, use the supported `--cyber-access-program daybreak_blue` option when starting the relevant session. Program selection does not grant access.

## Verification

Run `go test -mod=vendor ./...` for the skill catalog, manifest, TOML and installer checks, portable Git and delivery fixtures, and simulated Codex protocol regressions. The Git fixtures verify checkout behavior. For native skill discovery, configuration, sandbox and cancellation without credentials, use Codex CLI 0.172.0 and run `NSTACK_CODEX_NATIVE_TEST=offline go test -mod=vendor -v ./internal/codex -run '^TestNativeOffline'`. The separate `NSTACK_CODEX_NATIVE_TEST=oauth go test -mod=vendor -v ./internal/codex -run '^TestNativeOAuth$'` smoke requires authorization to use the existing OAuth account and runs Sol and Astra on disposable data. Session metadata identifies the configured model; it cannot independently establish the serving backend’s identity. See the [Japanese record](codex.ja.md) for historical live checks.

For comparisons, record elapsed time, agent runs, reviews, human interventions, and verified tasks using the same criteria. Codex JSONL usage includes input, cached input, and output tokens. Derive uncached input by subtraction. Keep session logs and IDs outside this repository. Comparative evidence is required for efficiency claims.

[Skills](https://learn.chatgpt.com/docs/build-skills) · [Subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents) · [Plugins](https://learn.chatgpt.com/docs/plugins) · [Configuration](https://learn.chatgpt.com/docs/config-file/config-reference) · [Daybreak in Codex CLI](https://help.openai.com/en/articles/20001607-using-daybreak-in-codex-cli)

## Role preflight

Read the installed role TOML and check discovery in the intended scope. Through an app-server for the same account and client, request `model/list` with `includeHidden: true` and collect all pages into one `{"data": [...]}` JSON result. Run `nstack check-role <installed-role.toml> < model-list.json`. For an inherited model, resolve it in the active session and pass `--inherited-model` and, if applicable, `--inherited-effort`. The catalog indicates advertised access; record a completed response separately when a live gate is required.

Copy `adapters/codex/optional/nstack_security_daybreak.toml` to the agents directory only if you intend to use that route. Explicitly select it for an authorized defensive task and pass `--daybreak-selected` to preflight. Require `availableAccessPrograms.cyber` to advertise `daybreakBlue`. Select the program for that session with `--cyber-access-program daybreak_blue` if required by the client. Keep the global program default off. If the role, model or access is unavailable, stop dispatch and report the reason. Model substitutions require an explicit choice.

## Automated independent review

Native custom agents can select a configured model and return results to their parent. Their effective sandbox and approvals are inherited. Use that route when the parent already has the required read-only permissions. A single parent with a writable implementation scope cannot enforce a read-only child review. The host entrypoint uses separate native app-server processes and ephemeral threads for the writer and each reviewer.

Run `nstack review` from an authorized host with Git, Codex CLI 0.172.0, and an existing ChatGPT OAuth login. Use an exclusive, clean feature checkout and a task brief containing observable acceptance criteria. The host must be able to write Codex's runtime state and start its normal sandbox. The adapter uses the normal Codex home and credential store. It reads the effective MCP configuration for each checkout, applies process-local Codex `-c` overrides, and verifies that every MCP server is disabled before dispatch. User and project files are preserved. Apps, plugins, hooks, memories, web search and native delegation are disabled for these child processes. The Codex sandbox restricts writes and network, and can allow reads outside the checkout. Use a separate restricted host account if you need stronger filesystem read isolation.

Native review requires Linux pidfds (including `waitid(P_PIDFD)`, available since Linux 5.4), subreaper support, and a procfs view of the current PID namespace with per-thread `children` files. Leave `CODEX_EXEC_SERVER_URL` unset so Codex executes locally within the managed process tree. The supervisor stops and reaps descendants before confirming teardown. Missing capabilities or unconfirmed cleanup block the workflow; see the error in `status.json` and any supervisor diagnostics in the stage's `.stderr` file. This requires neither root privileges nor cgroup delegation.

```sh
nstack review --authorize-host \
  --work-ref refs/heads/nstack-work-example \
  --repo /path/to/feature-checkout --base <base-sha> \
  --brief /path/to/acceptance.md --evidence /path/to/new-private-directory \
  --implement-model gpt-6.2-sol --implement-effort medium \
  --review-model gpt-6.2-astra --review-effort medium \
  -- /bin/sh -c 'GOCACHE="$TMPDIR/go-cache" GOTOOLCHAIN=local go test -mod=vendor ./...'
```

Use `--review-only` to verify an existing clean commit. Authorize writes with `--work-ref refs/heads/<work>`, a new local ref created only when writes are needed. The starting ref is retained. To authorize delivery, explicitly pass `--push <remote> --delivery-ref refs/heads/<feature>`, distinct from the local work ref. To update an existing PR only, add `--github-repo <owner/repo> --pr-base <branch> --pr-number <number>`. Use `--pr-title <title>` instead of the number to authorize finding or creating a draft.

Preflight checks effective settings, authentication, advertised model and effort, requested thread settings, actual review write denial, and the ability to write to the gate's dedicated scratch directory. It checks effective thread settings before sending prompts and rejects reported model reroutes. A clean checkout of each candidate runs the supplied gate with its own scratch space. Review starts in a separate checkout and context. A structured verdict must identify the full SHA. Demonstrated findings run through at most two fresh remediation rounds. Gate failures, missing evidence, blocked verdicts, repeated findings and permission or authentication errors stop delivery. The private evidence directory contains per-candidate gate outputs, model and sandbox settings, thread/turn IDs, structured verdicts, available usage counts and delivery status. It can contain repository content and should stay outside Git.

Delivery resolves one effective Git push destination and rejects an unverified remote HEAD. PR delivery requires the reviewed SHA range to match the PR merge base and head. Fetch the current base branch before running when that revision is missing locally. A changes-requested verdict in review-only mode exits nonzero and records `changes_requested` in `status.json`. Put the literal `--` before the gate command and install the tools that your gate runs. Existing Go builds named `nstack-codex` accept the same arguments.

The build vendors the TOML parser; its MIT notice is in [vendor/github.com/BurntSushi/toml/COPYING](../vendor/github.com/BurntSushi/toml/COPYING).

## Review an existing candidate before editing

Use `--review-first` from a clean checkout to gate and review its current commit. A first-pass approval creates no work branch. Before dispatching a writer, the controller checks the authorized work ref and, if delivery was requested, the effective remote default branch, delivery ref, starting SHA and PR. Fresh review assesses every previous finding ID as `resolved`, `recurring` or `uncertain` while independently discovering new defects. Recurrence, uncertainty and missing assessments stop further remediation. Because semantic matching can be imperfect, the hard maximum remains two rounds. Every review preserves its raw response and candidate/base SHA. Delivery rechecks the remote and PR before fast-forwarding; remote branch protections remain necessary for races at the server.

An authenticated host run of the implementation-first workflow used a disposable repository with Sol and Astra at medium effort. The gate passed for `0069da1` and Astra requested changes. Sol remediated the finding, the gate passed for `47d2db8`, and a fresh read-only Astra session approved that SHA. Both reviews recorded write denial. The run took 54.1 seconds and the four turns reported 153,421 input tokens, 90,624 of them cached, and 1,335 output tokens. This is evidence for the fixture run on nstack `3cf2feb`.
