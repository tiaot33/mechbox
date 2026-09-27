# "Install" means placing a skill in an Agent's directory

Until now the product used **Install** for bringing a skill *into* the Library and **Activation** for placing it *out* into an Agent's discovery directory (ADR 0001). The folder in the Agent directory was a **Projection**, bringing it back to the current version was a **Rewrite**, and looking at what a Source offers was a **Fetch** session. This ADR replaces that convention with the one the ecosystem already uses; the vocabulary itself lives in `CONTEXT.md`.

## Why the old convention went

- **"Install" meant the opposite of what users expect.** Every skills CLI and the Claude Code plugin flow use "install" for "put it where the Agent finds it; afterwards it works". Here, a user who clicked Install had a skill the Agent could not see, and had to find a second verb.
- **"Activate" sounds like licensing**, and collides with the Agent Skills spec, where activation means an Agent loading a skill's body at run time.
- **"Projection" and "Rewrite" were developer metaphors.** Users say "installed", "where is it installed", "update it to the new version"; nobody says "project".
- **"Fetch" did three jobs**: looking at a Source, the look itself as a session, and bringing skills into the Library. Sync's read-only report before push/pull was also called a "preview", colliding with the look at a Source.

## The decisions

| Code identifier | UI term | Meaning |
|---|---|---|
| Preview | 预览 | Look at the skills a Source offers, pinned to its content at that moment; nothing enters the Library |
| Candidate | 候选 | A skill in a Preview that can be chosen |
| Fetch | 入库 | Bring chosen Candidates from a Preview into the Library — the old Install |
| Install / Uninstall | 安装 / 卸载 | Choose to have a Library skill available in a Scope — the old Activate / Deactivate |
| Group install | 组安装 | The old group activation |
| Installation | 安装项 | The folder in a Scope and its record — the old Projection |
| Install plan | 安装计划 | Read-only report before Install — the old activation preview |
| Reinstall | 重新安装 | Bring an Installation back in line with a version — the old Rewrite |
| Sync plan | 同步计划 | Read-only report before push / pull — the old sync preview |

- **Fetch and Install are reused with their meanings reversed.** Any leftover old use would silently read as the new meaning and cannot be told apart by searching, so code, API, JSON, schema, comments, tests and docs switch together, and the product is unreleased, so no old names are kept. Identifiers for *reading content from a Source* (`fetchSource`, `maxFetchSize`, `Source.LastFetched`, JSON `last_fetched`) keep "fetch": that sense does not conflict with the new one.
- **Installation is a separate word from Install** because one folder can be backed by several choices at once — a direct Install and one or more Group installs. Uninstalling one choice removes the folder only when nothing else backs it.
- **"Plan" is the one word for read-only reports before execution** (Install plan, Sync plan); "Preview" is reserved for looking at a Source.
- **Schema tables and columns follow the terms** (`installations`, `group_installs`), and the schema version is bumped. Existing local libraries and Sync snapshots on S3 cannot be opened any more; they must be rebuilt (see ADR 0001, 2026-09-26: no migrations before release).

## Local directories go through Preview

The old `Install(LocalSource)` fetched one local folder directly, bypassing any Preview. With "new skills are fetched only from a Preview", a local directory is previewed like any remote Source:

- **The Source is the previewed directory** (absolute path), not each skill folder. A directory may hold several skills; they share one Source and so one Collection. Update and update checks rediscover the directory and relocate each skill by name, exactly as for remote Sources; a skill whose name disappeared from the directory fails its update with `NotFound`.
- **Names come from `SKILL.md`**, the same as every other Source. A local skill no longer takes its folder's name, and a folder without a valid frontmatter `name` and `description` is a diagnostic, not a Candidate.
- **The Preview is a snapshot.** Remote Sources are pinned by a commit or a downloaded archive; a local directory can change between Preview and Fetch, so Preview reads the Candidates' files into memory and Fetch stores exactly that snapshot. To keep a large directory from being read wholesale, the Candidates' files are capped at 1000 files and 64 MiB in total (the archive limits); exceeding either rejects the Preview with `SourceInvalid`.

## Considered and rejected

- **Keep the old words and explain them in the UI.** The mismatch is with every other tool the user touches; an explanation does not survive the first click.
- **New words only (e.g. "Deploy" for placing in a directory).** Avoids reversing Fetch and Install, but moves further from the ecosystem, which is the whole point.
- **Verify the local directory at Fetch instead of snapshotting at Preview.** Reads the directory twice and still needs a rule for what "unchanged" means; a snapshot matches how URL / Store archives already behave.

## Consequences

- JSON changes for integrators: fields `installed` → `fetched` (Candidate), `installed_at` → `fetched_at` (Skill, SkillSummary), `activated_at` → `created_at` (GroupInstall, matching `Installation.created_at`), `projected_copies` → `installations` (RemoveSkillOptions, UnregisterProjectOptions), `projections` → `unbacked_installations` (RemoveFromGroupOptions, which only affects Installations left without any backing and defaults to keep, unlike the other two); values `projected` → `already_installed` (install plan status, matching the group member outcome it predicts) and `installed` (group member outcome), `already_projected` → `already_installed`, `rewritten` → `reinstalled`.
- ADR 0001's statements that "Install writes into SQLite" and "Activation expands into the agent directory" are superseded by this ADR; other ADRs keep their decisions and now use the new words.
