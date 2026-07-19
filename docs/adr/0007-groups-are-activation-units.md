# Groups are activation units

A **Group** (a user-defined, scenario-based gathering of skills) is an *operation unit*, not just a label: a whole Group can be activated or deactivated into a Scope in one action. The Group itself needs only two tables — `groups` and `skill_groups` (many-to-many). Groups are flat (no nesting) and reference skills by identity (they do not pin a version). How whole-group activate/deactivate lands is part of the Activation layer.

A Group is orthogonal to a **Collection**: a Collection groups skills automatically by Source, while a Group is a manual, cross-Source gathering by scenario (e.g. "frontend debugging"). The whole point of hand-picking skills into a pile is to use that pile *as a unit* — otherwise a Group has no value beyond what a Collection already gives. Hence: operation unit.

## Consequences

- `groups` (id, name) + `skill_groups` (skill_id, group_id); many-to-many — a skill can be in several Groups.
- Groups are flat; no group-within-a-group.
- A Group references a skill's identity, not a specific version (pin/follow is a later Activation-behaviour decision).
- Activating a Group expands to one activation record per member skill; how deactivation tracks "which activations came via this Group" belongs to the Activation layer (to be specified).
- **Collection** stays derived (no table), as the automatic-by-Source counterpart to the manual-by-scenario Group.
