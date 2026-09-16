# Groups are activation units

A **Group** (a user-defined, scenario-based gathering of skills) is an *operation unit*, not just a label: a whole Group can be activated or deactivated into a Scope in one action. The Group itself needs only two tables — `groups` and `skill_groups` (many-to-many). Groups are flat (no nesting) and reference skills by identity (they do not pin a version). How whole-group activate/deactivate lands is part of the Activation layer.

A Group is orthogonal to a **Collection**: a Collection groups skills automatically by Source, while a Group is a manual, cross-Source gathering by scenario (e.g. "frontend debugging"). The whole point of hand-picking skills into a pile is to use that pile *as a unit* — otherwise a Group has no value beyond what a Collection already gives. Hence: operation unit.

## Consequences

- `groups` (id, name UNIQUE, created_at) + `skill_groups` (skill_id, group_id); many-to-many — a skill can be in several Groups.
- Group names are unique. Create / rename / migrate on a collision append `#N` (smallest free N starting at 2), case-sensitive, no extra space. The operation does not fail.
- Groups are flat; no group-within-a-group.
- A Group references a skill's identity, not a specific version (pin/follow is a later Activation-behaviour decision).
- Activating a Group expands to one `activations` record per member skill. `activation_groups` records each Group that contributed to that projection, while `activations.individual` records a non-Group reason to retain it, normally an independent activation.
- Deactivating a Group removes only that Group's provenance. The projected skill remains while an independent activation or another Group still contributes to it.
- Deleting a Group is non-destructive: a projection contributed only by that Group becomes individually retained before the Group provenance is removed.
- **Collection** stays derived (no table), as the automatic-by-Source counterpart to the manual-by-scenario Group.
