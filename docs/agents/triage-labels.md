# Issue Labels

Every issue carries three kinds of label: exactly one **type**, one or more **areas**, and exactly one **triage** state. Apply all three when creating an issue.

## Type

What kind of change the issue asks for.

| Label           | Use when                                                                 | Examples                     |
| --------------- | ------------------------------------------------------------------------ | ---------------------------- |
| `feature`       | Users gain a new capability, or existing behaviour changes to meet a need | update by collection, single fetch input |
| `bug`           | Existing behaviour differs from what was intended                        | a folder deleted while another operation runs |
| `performance`   | Behaviour stays the same; it gets faster or makes fewer requests         | fewer GitHub API calls       |
| `refactor`      | User-visible behaviour stays the same; code layout, API shape or naming changes | reorganising packages, renaming terms in code |
| `documentation` | Only documents change                                                    | terminology docs             |

## Area

Which part of the code the issue touches. Add every area that applies.

| Label           | Covers                                                                                   |
| --------------- | ---------------------------------------------------------------------------------------- |
| `area:desktop`  | The desktop app under `cmd/desktop`                                                      |
| `area:api`      | The library's public API in `internal/library`: methods or types the desktop app calls change, so the desktop app has to follow up |
| `area:backend`  | The library's internals (`internal/library/internal/...` and private code in `internal/library`), with the public API unchanged |

An issue that changes a public request type and also adds a new source package gets both `area:api` and `area:backend`.

## Triage

The skills speak in terms of five canonical triage roles. This table maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

Edit the right-hand column to match whatever vocabulary you actually use.
