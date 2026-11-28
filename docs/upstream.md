# Upstream, licenses, and synchronization

[日本語](upstream.ja.md)

nstack is an independent Codex adaptation of Lauren Tan (Poteto)’s [pstack 0.15.48](https://github.com/cursor/plugins/tree/b7b93c36be9bcc36a74db438e0fd385d660bc559/pstack), pinned at revision `b7b93c36be9bcc36a74db438e0fd385d660bc559`. The 12 principle `SKILL.md` files retain their original text. The other skills and playbooks are shortened Codex adaptations of upstream workflows. The adapter follows upstream’s Claude Code approach of mapping host terms and capabilities. Upstream generators, hooks, Bun tools, and the PR watcher are outside the distribution.

## Port comparison

The public archives were inspected at the pinned revisions below. The comparison covered README files, manifests, model routing, execution, and validation. No port executables were run or incorporated.

| Port | Design and tradeoffs |
|---|---|
| [nahuelb](https://github.com/nahuelb/pstack-codex/tree/121c6fb0c47135a02ef927f76da76a3dbf176ff1) | Upstream lock, compatibility report, CLI marketplace, role registry, worktree audits, head-based checks, install smoke tests, hooks, and Benny automation. |
| [atlys-hein](https://github.com/atlys-hein/pstack-codex/tree/b7bf49e08f756db153be29c6611bbb8d9d1136b5) | Import package based on upstream 0.15.36, with model and concurrency instructions, resource maps, local checkpoints, and host-enforced permissions. |
| [theomilll](https://github.com/theomilll/pstack-codex/tree/270c71fa27e0f5128a073424e3e6fe3042606555) | CLI marketplace, upstream sync checks, native subagents, worktree audits, independent verification, and helpers. Model and reasoning effort follow the parent. |
| [painhardcore](https://github.com/painhardcore/pstack/tree/af212355c0a8f21d2545f20042628f3911048a4f) | Patch-based sync for Codex, Claude Code, and OpenCode. Includes marketplace and skills CLI installation, serial fallback, and project verification skills. |
| [bemoat](https://github.com/bemoat/pstack/tree/dc8e617f179cf0bdc68b61e672f33e9eb64c20cf) | Multi-host generator, model settings, worktree and resume helpers, and generator, sync, and model tests. Wider host support increases the maintenance surface. |

The root license for the pstack portions of all five ports is MIT. bemoat also credits Michael Denyer and carries a separate Cursor Team Kit license. This distribution uses upstream pstack directly and preserves Lauren Tan’s copyright and permission notice in [LICENSE-upstream](../LICENSE-upstream).

## Synchronize

1. Compare the recorded revision with the new upstream SHA, including pstack, the generator, manifests, and licenses.
2. Copy relevant updates to adopted principles from the same relative paths. Apply workflow changes only to the affected Codex skills.
3. Inspect any new commands or external connections for permissions, destinations, and licensing.
4. Update the provenance and notices, run `go test -mod=vendor ./...`, and smoke-test the affected behavior.
