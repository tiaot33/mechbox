# Agent Skills background

Ecosystem facts only: what an **Agent Skill** is, how coding agents discover and load them, and how people put them on disk today.

This is **not** a design doc for this product. Do not treat anything here as a decision about Library, Install, Activation, storage, or runtime integration. Product language and decisions live in root `CONTEXT.md` and `docs/adr/` — and only once they are actually written there.

## What is a skill?

A **skill** is **one directory** that a coding agent can load on demand. It is not a plugin binary, not an MCP server, and not a one-off prompt paste.

### On-disk shape

| Rule | Detail |
| --- | --- |
| **Unit** | One skill = one folder (the skill root). |
| **Required entry** | That folder **must** contain a `SKILL.md` at its root. No `SKILL.md` → not a skill. |
| **Optional rest** | Any other files and subfolders are allowed — scripts, references, assets, examples, nested helpers, etc. |
| **Many skills** | An agent’s skills directory holds **many** such folders side by side; each sibling folder is a separate skill with its own `SKILL.md`. |

Minimal skill (only the required file):

```text
my-skill/
└── SKILL.md
```

Richer skill (common layout; names like `scripts/` are convention, not a hard limit):

```text
my-skill/
├── SKILL.md              # required entrypoint
├── scripts/
│   └── helper.py
├── references/
│   └── api.md
├── assets/
│   └── template.md
└── examples/
    └── sample.md
```

Agents discover skills by scanning for these folders; they read `SKILL.md` first, then open other files in the same tree only when the instructions say so (or when the task needs them).

The open format is **[Agent Skills](https://agentskills.io)** (`SKILL.md` + progressive disclosure). Claude Code, Codex, Cursor, Gemini CLI, and many other agents implement it (with product-specific extras).

### `SKILL.md` shape

```markdown
---
name: my-skill
description: What it does and when to use it. Agents match tasks against this text.
---

# Instructions

Step-by-step guidance the agent follows once the skill body is loaded.
Point at supporting files instead of inlining huge references.
```

- **`name`**: stable identifier (spec: lowercase, hyphens; often matches the directory name).
- **`description`**: discovery text. Agents keep only this (plus the name) in context until the skill is needed — write *what* and *when*.
- **Body**: full procedure, loaded when the skill is used.

Products may add extra frontmatter (e.g. Claude Code’s `disable-model-invocation`, `allowed-tools`). The portable core is `name` + `description` + body.

## How an agent uses a skill

Agents do **not** register skills via an API. They **scan directories on disk**, then load content in stages (**progressive disclosure**):

1. **Discovery** — at session start, load only each skill’s `name` + `description` (~metadata tokens).
2. **Load** — when the user task matches a description (or the user invokes the skill explicitly), read the full `SKILL.md` into context.
3. **Execution** — follow the instructions; open `scripts/` / `references/` / `assets/` only as needed.

Two common invocation paths:

| Path | What happens |
| --- | --- |
| **Model-driven** | The agent sees the skill list in its system/tool context and decides the task matches `description`. |
| **User-driven** | The user types a slash command such as `/my-skill` (Claude Code: directory name under `skills/`; similar menus in other clients). |

Practical consequence of the ecosystem (not a product design claim):

- An agent only sees a skill if that skill’s **folder is present** under a directory the agent scans.
- Putting the folder there (copy, symlink, or whatever the user/tool did) is what “install into the agent” means in agent docs. Some products also have settings that hide or disable a skill by name.

## How people put skills on disk today

In everyday agent documentation, “install a skill” usually means **place the skill folder where the agent looks**. Common layouts:

| Agent | Personal (all projects) | Project-only |
| --- | --- | --- |
| **Claude Code** | `~/.claude/skills/<skill-name>/SKILL.md` | `<repo>/.claude/skills/<skill-name>/SKILL.md` |
| **Codex** | `~/.codex/skills/<skill-name>/SKILL.md` | `<repo>/.codex/skills/<skill-name>/SKILL.md` |

Common manual flows:

- `git clone` a skills repo, then copy or symlink one skill directory into a path above.
- Commit a skill under the repo’s project skills folder for the team.
- Use a product’s own installer / marketplace / plugin (still ends as folders + `SKILL.md` on disk).

## Further reading (ecosystem)

- Spec & overview: [agentskills.io](https://agentskills.io), [specification](https://agentskills.io/specification)
- Claude Code: [Extend Claude with skills](https://code.claude.com/docs/en/skills)
