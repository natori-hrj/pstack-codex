# Migration and rollback

## From pstack-codex v0.1.0

[日本語](migration.ja.md) · [Installation](../README.md)

nstack replaces `$poteto-mode` with `$natori-mode`, `$setup-pstack` with `$setup-nstack`, and the three `pstack_*` agent names with `nstack_*`. The 24 other skill names are shared. Keep only one distribution active in any session.

## Inventory before changing anything

- Run `codex plugin list` and record any enabled `pstack-codex@<marketplace>` selector and its source. Keep the original v0.1.0 checkout available for rollback.
- Check `${CODEX_HOME:-$HOME/.codex}/skills/` and `${CODEX_HOME:-$HOME/.codex}/agents/` for direct copies. The two old skill directories, `pstack_{implement,security,review}.toml`, and `pstack-codex-LICENSE*` are useful indicators. The full v0.1.0 skill inventory is available from `git ls-tree --name-only v0.1.0:skills` in a checkout with that tag.
- Check active project skill and agent directories if you also installed there. Compare prospective names with enabled plugin skills, skill frontmatter names, and agent TOML `name` fields, including files with different filenames. Treat existing `natori-mode`, `setup-nstack`, `nstack_*`, and same-name copies of shared skills as conflicts until their ownership is clear. Leave unrelated files in place. Resolve any ambiguous name before proceeding.

## Direct installation

1. Close active Codex sessions. If the inventory found an enabled old plugin, remove it with `codex plugin remove pstack-codex@<marketplace>` using its recorded selector. Keep its marketplace and source for rollback. Make a backup directory outside all Codex skill and agent search paths. Move the verified v0.1.0 skill directories, three old agent TOMLs, and the two old distribution license files there, keeping their relative paths. If a shared skill was customized, compare it with the tagged original before moving it. Keep the customizations in the backup and consciously reconcile them with the new copy. In particular, keep the new playbook paths in the `babysit`, `autopilot-stack`, `session-pickup`, and `pause-safely` wrappers.
2. Follow the [nstack installation instructions](../README.md) from the new checkout. Install the complete skill set at the intended scope so that shared skill names resolve to the same distribution.
3. In each new `nstack_implement.toml`, `nstack_security.toml`, and `nstack_review.toml`, carry over the matching old file’s customized `model` and `model_reasoning_effort`. Preserve intentional omissions so inherited settings still apply. Keep the new filenames and `name` fields. Review any other custom settings individually.
4. Start a new Codex session and run `$setup-nstack`. Check that `$natori-mode` and `$setup-nstack` are each available once and the old names are absent. Verify your three model and reasoning settings and update saved prompts. The direct install leaves Codex configuration and credentials in place.

## Native plugin installation

1. Close active sessions. Run `codex plugin remove pstack-codex@<marketplace>` using the exact selector from the inventory. Keep the old marketplace registration and source for rollback. Move any old direct skill copies out of discovery using the backup step above. This also applies to a mixed installation.
2. [Register the nstack marketplace and install `nstack@nstack-local`](skills.md#other-codex-installation-options). Use a separate checkout or marketplace root from the saved old one. Plugin commands update marketplace and enablement entries through Codex.
3. Move the old custom agent TOMLs and old direct-install license files to the backup. Install the new agent files separately and transfer model settings as in the direct path. The plugin supplies the skills and licenses. Start a new session, check for `$nstack:natori-mode` and `$nstack:setup-nstack`, and verify your model settings. Update prompts that use the old plugin namespace. During plugin rollback, use the restored `pstack-codex:` namespace.

## Roll back

Close Codex sessions. If the destination uses the nstack plugin, remove `nstack@nstack-local`. If the **original** setup used the old plugin, reinstall its recorded selector from the original marketplace, even when migrating to a direct install. Remove the new marketplace registration only if it was added for this migration and is unused. For a direct install, move the installed nstack skill directories out of discovery. In either case, move the new agent files and any new direct-install license files out of the way, then restore the backed-up files to their original paths. For a previously mixed install, restore the backed-up direct files as well. Reopen Codex, select the restored workflow and setup skills in the skill picker, and verify the restored model settings.

## From direct copies or a plugin to a managed snapshot

For the historical Python release, see the [v0.3.0 instructions](v0.3.0.md). Keep prior snapshots for rollback.

Use the [standalone binary](../README.md#standalone-go-binary) to stage and activate the new snapshot. Role examples remain in the staged `examples/agents` directory; existing installed role TOMLs are not replaced.

For old direct copies, stage alongside the installation. After verifying the candidate in a disposable home, back up old skill directories by moving them out of all search paths, then activate the new snapshot. To roll back to the legacy layout, move the managed symlink out of discovery and restore the saved directories. For old plugins, preserve their source and selector, then disable them before activating direct skills. Restore their original selector for a plugin rollback.

Manage agent TOMLs independently of skills. Inspect your existing `nstack_security` TOML, since v0.2.0 shipped a Daybreak assignment. Back it up before switching to the new standard example. Apply your intended custom model and effort settings separately. Restore the backup for role rollback. Check project and plugin scopes for duplicate skill and role names before activation.

## From v0.4.0 or a Python-managed snapshot to v0.4.1

Keep the same Codex home, active link, previous snapshots and customized role TOMLs. Record the active snapshot ID before updating. With the v0.4.1 binary, run `nstack install --home "$config_dir" stage <new-id>` and `nstack install --home "$config_dir" activate <new-id>`. To roll back, use the v0.4.1 binary to activate the recorded previous ID, then select the older executable separately if you also need to roll back the CLI. v0.4.0's Go installer expects files that were removed from the v0.4.1 payload, so use v0.4.1 when reactivating its own snapshot.

The installers share the same store and lock. The v0.4.1 binary verifies all bytes of sealed Go snapshots. For unsealed Python-era snapshots it checks the legacy required files, permissions, and skill metadata and instructions without running the old scripts. Keep those snapshots intact and use a distinct ID for each release. For v0.4.0 OpenCode homes use the same steps with `--harness opencode` and the [separate OpenCode configuration directory](opencode.md).
