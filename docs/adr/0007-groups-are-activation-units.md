# Groups are install units

*(Terminology updated 2026-09-27 per [ADR 0014](0014-install-means-agent-directory.md): activation → Install, group activation → Group install, Projection → Installation, Rewrite → Reinstall. The decisions are unchanged; the file name keeps the old wording so links stay valid.)*

A **Group** (a user-defined, scenario-based gathering of skills) is an *operation unit*, not just a label: a whole Group can be installed into or uninstalled from a Scope in one action. The Group itself needs only two tables — `groups` and `skill_groups` (many-to-many). Groups are flat (no nesting) and reference skills by identity (they do not pin a version). How whole-group install/uninstall lands is part of the install layer.

A Group is orthogonal to a **Collection**: a Collection groups skills automatically by Source, while a Group is a manual, cross-Source gathering by scenario (e.g. "frontend debugging"). The whole point of hand-picking skills into a pile is to use that pile *as a unit* — otherwise a Group has no value beyond what a Collection already gives. Hence: operation unit.

## Consequences

- `groups` (id, name UNIQUE, created_at) + `skill_groups` (skill_id, group_id); many-to-many — a skill can be in several Groups.
- Group names are unique. Create / rename on a collision append `#N` (smallest free N starting at 2), case-sensitive, no extra space. The operation does not fail.
- Groups are flat; no group-within-a-group.
- A Group references a skill's identity, not a specific version (pin/follow is a later install-behaviour decision).
- Installing a Group expands to one Installation per member skill. Each Installation is backed by installs: a direct Install of the skill (recorded on the Installation), and/or the Group installs that cover it — derived from membership and the Groups' installs, not stored per Installation (see 2026-09-25; terms in `CONTEXT.md`).
- Uninstalling a Group withdraws only that Group install. An Installation remains while a direct Install or another Group install still backs it.
- Deleting a Group is non-destructive: an Installation backed only by that Group gets a direct Install before the Group install is removed.
- **Collection** stays derived (no table), as the automatic-by-Source counterpart to the manual-by-scenario Group.

## 2026-09-25: a Group install is recorded, and membership follows it (#85)

Originally "the Group is installed in this Scope" was only inferred from which Installations carried that Group, so changing membership left Installations stuck behind a Group they no longer belonged to, and new members were never installed. The Group install is now a record of its own — Group × Scope — written by `InstallGroup` (also for an empty Group) and removed by `UninstallGroup`, `DeleteGroup` and unregistering the project. Which Groups cover an Installation is no longer recorded on it but derived from membership and these records: the skill is a member, and the Group is installed in that Scope.

- **A Group install covers its current and future members.** Adding a skill installs it into every Scope the Group is installed in. Where the skill is already installed there, only the Group install is added and the folder is not written. Adding a member has no overwrite switch — an untracked folder always skips that Scope — because one switch would apply to every Scope the Group is installed in; to overwrite in one Scope the app shows the install plan and calls `InstallGroup` with `Overwrite` for that Scope only.
- **Removing a member** takes it out of the Group's coverage. By default (`keep`) its Installation gets a direct Install in place of the Group's, with no disk change — the same non-destructive rule as deleting a Group. With `withdraw` nothing replaces it, and an Installation left without any install is removed.
- **Conflicts skip a member, not the Group.** `InstallGroup` and adding a member install what they can; a member whose target is occupied, holds an untracked folder, or shares its name with another member and is not yet installed is skipped (all such members, not first come first served; the one already installed only gains the Group install) and reported per member. Membership and the Group install still stand, and installing the Group again fills the gaps. This replaces the earlier all-or-nothing group install: the user's intent is "this Group applies here", and one conflicting member should not veto it.
- **Installing never overwrites an existing Installation.** Installing a skill or Group where an Installation already exists only adds an install; bringing the folder up to the current version is a separate Reinstall (#86), triggered by the app.
