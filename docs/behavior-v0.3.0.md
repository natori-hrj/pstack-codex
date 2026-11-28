# v0.3.0 behavior baseline

This is the compatibility target for the Go migration: `v0.3.0`, commit
`bb8206a95bffb8908d75be8538e3e106bf280266`. It describes the shipped Python
entry points; the fixtures below are test interfaces, not new runtime commands.

Historical commands in this document are run from the matching published tag.
The last Python/Go comparison suite is in [v0.4.0](https://github.com/natori-hrj/nstack/tree/v0.4.0/tests).
In v0.4.1 the corresponding contract tests run with `go test -mod=vendor ./...`.

## Install and role preflight

| Entry point | Inputs and observable behavior |
|---|---|
| `python3 adapters/codex/install.py --home PATH {stage,activate} ID` | ID starts with an ASCII letter/digit and otherwise permits letters, digits, dots, `_` and `-`. Success prints the snapshot path and exits `0`; argument errors exit `2`, handled filesystem/install failures exit `1`. The chosen home is explicit; the README's shell wrapper defaults to `${CODEX_HOME:-$HOME/.codex}`. |
| `python3 adapters/codex/check_role.py ROLE.toml [--inherited-model MODEL] [--inherited-effort EFFORT] [--daybreak-selected]` | Reads the complete model catalog as JSON on stdin, either `{"data":[...]}` or `{"result":{"data":[...]}}`; rejects a remaining pagination cursor. Resolves installed settings before inherited values and verifies the model and requested effort. Hidden models are rejected for ordinary roles. The optional Daybreak route requires explicit selection and advertised access. Success prints `ROLE: MODEL advertised (provider response unverified)` and exits `0`; an unavailable or invalid role/catalog exits `1`, argument errors exit `2`. |

`stage` copies the skills, docs, adapters, example roles, both README files and
both MIT notices to `HOME/nstack/snapshots/ID`. Restaging identical content is
idempotent; the same ID with different content fails. `activate` switches the
managed `HOME/skills/nstack-managed` symlink to the staged `skills` directory
under the `HOME/nstack/lock` lock. It rejects unmanaged paths and duplicate
skill names, including names declared in YAML frontmatter. Staging alone
does not affect discovery; activating a previous ID rolls back. The installer
does not change user role TOMLs. The README copies examples only to absent
paths after both install steps succeed. Plugin layout and defaults are covered by
the [Codex adapter](codex.md) and [migration guide](migration.md).

## Host review CLI

The entry point is `python3 adapters/codex/review.py`. It requires
`--authorize-host`, `--repo PATH`, `--base REV`, `--brief FILE`,
`--evidence PATH` and a gate command as argv after `--`. The repo must be its
top-level checkout. The base must be an ancestor of the candidate, and the
evidence directory must be new and outside the checkout. Dirty worktrees and
index flags that can conceal modifications block. The per-checkout lock lives
at Git's `nstack-review.lock` path.

| Option or mode | Contract |
|---|---|
| Default | Implementation requires a new, explicitly authorized `--work-ref refs/heads/...`. The coordinator owns commits; the original ref must not move. |
| `--review-only` | Gate and review the current candidate without writing it or creating a writer ref. Demonstrated changes requested stop with that distinct status. |
| `--review-first` | Gate and review the current candidate first. A pass needs no writer. Only demonstrated findings can trigger a writer on an authorized new work ref. Mutually exclusive with `--review-only`. |
| Models and limits | `--implement-model` defaults to `gpt-6.2-sol`, `--review-model` to `gpt-6.2-astra`, both `--*-effort` to `medium`. `--timeout` is positive and defaults to 600 seconds per Codex process. `--max-remediations` accepts 0–2, default 2. |
| Delivery | `--push REMOTE` and `--delivery-ref REF` are paired. PR delivery additionally needs `--github-repo`, `--pr-base` and one of `--pr-title` or positive `--pr-number`. Pin the effective destination and verify it before delivery. Push only the passing SHA by normal fast-forward to an authorized feature ref, without following tags. The CLI rejects `main`, `master`, `trunk`, the remote default and the specified PR base as delivery refs. Other branch protections are enforced by the remote; the CLI does not query those rules. |
| PR selection | `--pr-title` selects the unique open same-repository PR already using the authorized head, or creates a draft after push if no such PR exists. Multiple matches or mismatched repo/base/head block. `--pr-number` must identify the matching, verified existing PR; it does not override ambiguity. Verify remote SHA and PR merge base and head. No automatic merge or release. |

Exit `0` prints success with candidate and evidence location. Exit `2`
reports argument errors. Handled workflow failures exit `1`: `status.json`
distinguishes `blocked` from `changes_requested` in review-only mode. A
failure before creating usable evidence (e.g. an existing evidence directory)
need not produce a status file. Failed gates prevent dispatch of the
corresponding review. Missing required permissions, credentials, role,
schema or evidence also block. Previously recorded findings must each be
assessed on a new candidate; recurring or uncertain findings stop remediation.

### Evidence and side effects

- `request.json`: adapter, starting/base SHA, selected roles, gate argv,
  mode, budget, original/work/delivery refs and PR number.
- `preflight.json`: effective adapter capabilities and probe results.
- `<round>-implementation.json`, `<round>-gate.json`, `<round>-review.json`:
  artifacts for stages actually reached. Implementation records its start/base
  and work ref. Gate and review pin candidate/base. Review includes adapter
  identifiers and accepted verdict with finding IDs, or a `validationError`
  for a malformed or mismatched result. Codex stage stderr is separate.
- Verdicts require `candidate`, `verdict`, and `findings`; tracked reviews
  also require `previous`. Verify the full SHA, fresh context, verdict/finding
  consistency and complete prior-ID assessment. Gate and review use separate
  disposable checkouts of the candidate.
- `status.json`: terminal status and elapsed time, plus candidate on pass or
  review-only changes requested and error on failure. `delivery.json` is
  written only when requested delivery completes verification and contains
  candidate/base, destination and default refs, and the PR URL if applicable.
  If push succeeded but a later PR action failed, absence of `delivery.json`
  does not prove the remote ref is unchanged.

Parallelism is optional only when the caller can meet the same required
permissions and capabilities in serial. If isolation or another mandatory
capability cannot be verified, stop the affected work and retain its evidence.
An independent review or native schema requirement is not satisfied by
running serially or parsing a reply as JSON locally.

## Verification and comparison

- `python3 tests/validate.py --suite common`: shared assets, deterministic
  Git worktree checks and portable workflow fixtures. Requires Python 3.11+
  and Git, with no harness, PyYAML or provider credentials. Git fixture
  worktrees do not prove agent sandbox isolation.
- `python3 tests/validate.py --suite codex`: plugin and example-role checks,
  install/upgrade/rollback and native skill discovery, native configuration
  and the existing fake app-server/workflow regressions. Requires PyYAML and
  Codex CLI 0.172.0, but not provider authentication. Native and simulated
  results are reported separately from any authenticated model evidence.
- `python3 tests/validate.py`: runs both suites, as before for a full gate.
- `python3 tests/compatibility.py [-- /absolute/path/to/test-launcher ...]`:
  runs the same black-box fixture scenarios against the supplied argv
  prefix or the bundled Python fixture launcher. Each invocation receives
  the legacy review argv in a private temporary Git fixture, with the case in
  `NSTACK_CONTRACT_SCENARIO`. The test launcher must select a deterministic
  adapter named `portable-fixture` implementing the behavior in
  [`tests/fixtures/portable_review.py`](https://github.com/natori-hrj/nstack/blob/v0.4.0/tests/fixtures/portable_review.py);
  it should not start a live harness. The runner checks real exit codes, refs,
  files and candidate-bound evidence without requiring stable temp paths,
  Git object IDs or elapsed time. Use the same runner for both launchers as
  the Go port lands. The runner alone does not claim full parity.

The G2 comparison command is `python3 -B tests/go_compatibility.py` (Go 1.22+
and Git). It uses the Go CLI with an injected, test-only adapter: the portable
G1 cases, matching PR delivery artifacts and Git side effects on local bare
remotes, and the existing extended cases through the simulated app-server
fixture. No native harness executable, account, or network is used in these
comparison cases. `go test ./...` additionally exercises cancellation,
timeouts and rejection of unverified evidence. The Go core has no default
production adapter; native process, permissions and authenticated verification
remain separate adapter gates.

### Intentional Go input differences

- **Gate boundary and authorization:** Python's `argparse` can accept a gate
  without `--` and interpret a trailing `--review-only` as a workflow flag.
  Go requires the documented `--` before the gate argv and full workflow
  option names. For example, `true --review-only` without the separator
  exits `2` before dispatch, ref changes, or evidence creation. The
  documented `--review-only -- true` form still reviews without a writer.
  This avoids accidentally authorizing the default implementation route
  when a caller mixes workflow flags and gate arguments.
- **Evidence:** Go rejects duplicate JSON member names in a verdict instead
  of letting a later value hide an earlier one. A missing gate exit code
  or missing or mismatched turn identity cannot produce a pass. Rejected
  results block review or delivery; a reached review attempt retains its
  candidate, base, returned message, and validation error.
  A role selected for dispatch must have a nonblank model and reasoning
  effort; empty values cannot establish the required role identity.
