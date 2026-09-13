# Skills Manager · 技能管理器

A TUI for managing coding-agent skills: installing them from source repos, organising them, and activating them into the directories that coding agents read from.

Skills here are **[Agent Skills](https://agentskills.io)** — folders with a `SKILL.md` that agents discover from disk and load on demand. For format, discovery scopes, invocation, and same-name behavior, see [ecosystem background](docs/agents/agent-skills-background.md). Product terms stay in this file; integration boundaries and implementation gaps are recorded in [ADR 0001](docs/adr/0001-sqlite-as-the-library.md).

> Term headings are bilingual: the **English** name is the canonical identifier used in code; the **中文** name is the term used in the v1 (Chinese-only) UI. See `docs/adr/0003`.

## Language · 术语

**Skill · 技能**:
A package of instructions and optional resources with a root `SKILL.md`, which a coding agent discovers from disk and loads on demand.
_Avoid_: Plugin, extension, prompt / 插件、扩展、提示词

**Agent · 智能体**:
A coding agent with a stable code and a display name, loading skills from its defined global and Registered project directories; changing its name does not change its identity. The Library holds each Agent and its current layout, whose directory names users do not choose; v1 targets Claude Code and Codex only.
_Avoid_: Assistant, tool, client / 助手、工具、客户端

**Library · 库**:
The tool's own authoritative store of skills plus their metadata (collections, groups, activation state). The single source of truth; agent directories are projections of it.
_Avoid_: Store, database, cache, registry / 存储、数据库、缓存、注册表

**Source · 来源**:
The origin a skill was installed from — typically a git repo.
_Avoid_: Origin, remote, provider / 源头、远端、提供方

**Install · 安装**:
Fetching a skill from its Source into the Library. Distinct from Activation: Install brings a skill *into* the tool; Activation places it *out* into an agent's directory.
_Avoid_: Download, add, import / 下载、添加、导入

**Collection · 集合**:
Skills grouped by their Source (i.e. by source repo). Automatic — a skill's collection follows from where it came.
_Avoid_: Repo group, bundle / 仓库分组、捆绑包

**Group · 组**:
A user-defined gathering of skills by scenario (e.g. "frontend debugging"). Orthogonal to Collection.
_Avoid_: Tag, category, set / 标签、分类、集

**Scope · 作用域**:
One of two kinds: an Agent's global level, or an Agent together with one Registered project. Chosen by kind, never by directory.
_Avoid_: Target, context, location, path / 目标、上下文、位置、路径

**Registered project · 已登记项目**:
A project directory the tool has been told to manage, kept in a list in the Library so project-level Scopes are first-class and activation state across projects is visible in one place.
_Avoid_: Workspace, folder, repo / 工作区、文件夹、仓库

**Activation · 激活**:
Making a skill available in a Scope by placing its folder into the directory the Agent currently defines for that Scope. Deactivation removes it. Former directories after a layout change are stale. The Library remains the source of truth throughout.
_Avoid_: Install, enable, deploy / 安装、启用、部署

**Self-developed skill · 自研技能**:
A skill whose Source is local and owned by the user — authored in place rather than installed from a remote repo.
_Avoid_: Custom skill, local skill / 自定义技能、本地技能

**Sync · 同步**:
Manual, non-realtime push/pull of the whole Library to and from S3, so a full setup can be reproduced on another machine. Never automatic. Machine-specific paths (Registered projects) are carried as advisory values and may be remapped per machine.
_Avoid_: Backup, replication, upload / 备份、复制、上传
