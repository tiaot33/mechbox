# Groups are activation units

A **Group** (a user-defined, scenario-based gathering of skills) is an *operation unit*, not just a label: a whole Group can be activated or deactivated into a Scope in one action. The Group itself needs only two tables — `groups` and `skill_groups` (many-to-many). Groups are flat (no nesting) and reference skills by identity (they do not pin a version). How whole-group activate/deactivate lands is part of the Activation layer.

A Group is orthogonal to a **Collection**: a Collection groups skills automatically by Source, while a Group is a manual, cross-Source gathering by scenario (e.g. "frontend debugging"). The whole point of hand-picking skills into a pile is to use that pile *as a unit* — otherwise a Group has no value beyond what a Collection already gives. Hence: operation unit.

## Consequences

- `groups` (id, name UNIQUE, created_at) + `skill_groups` (skill_id, group_id); many-to-many — a skill can be in several Groups.
- Group names are unique. Create / rename / migrate on a collision append `#N` (smallest free N starting at 2), case-sensitive, no extra space. The operation does not fail.
- Groups are flat; no group-within-a-group.
- A Group references a skill's identity, not a specific version (pin/follow is a later Activation-behaviour decision).
- Activating a Group expands to one Projection per member skill. Each Projection records which Activations back it: a direct activation of the skill, and/or the Activations of Groups that cover it (terms in `CONTEXT.md`).
- Deactivating a Group withdraws only that Group's Activation. A Projection remains while a direct activation or another Group's Activation still backs it.
- Deleting a Group is non-destructive: a Projection backed only by that Group gets a direct activation before the Group's Activation is removed.
- **Collection** stays derived (no table), as the automatic-by-Source counterpart to the manual-by-scenario Group.

## 2026-09-25: a Group's Activation is recorded, and membership follows it (#85)

Originally "the Group is active in this Scope" was only inferred from which Projections carried that Group, so changing membership left Projections stuck behind a Group they no longer belonged to, and new members were never projected. The Group's Activation is now a record of its own — Group × Scope — written by `ActivateGroup` (also for an empty Group) and removed by `DeactivateGroup`, `DeleteGroup` and unregistering the project.

- **A Group's Activation covers its current members.** Adding a skill projects it into every Scope the Group is activated in. Where the skill is already projected there, only the Group's Activation is added and the folder is not written.
- **Removing a member** takes it out of the Group's coverage. By default (`keep`) its Projection gets a direct activation in place of the Group's, with no disk change — the same non-destructive rule as deleting a Group. With `withdraw` nothing replaces it, and a Projection left without any Activation is removed.
- **Conflicts skip a member, not the Group.** `ActivateGroup` and adding a member project what they can; a member whose target is occupied, holds an untracked folder, or shares its name with another member is skipped and reported per member. Membership and the Group's Activation still stand, and activating the Group again fills the gaps. This replaces the earlier all-or-nothing `ActivateGroup`: the user's intent is "this Group applies here", and one conflicting member should not veto it.
- **Activating never rewrites an existing Projection.** Activating a skill or Group where a Projection already exists only adds an Activation; bringing the folder up to the current version is a separate Rewrite (#86), triggered by the app.
