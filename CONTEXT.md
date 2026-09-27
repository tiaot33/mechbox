# Skills Manager · 技能管理器

A tool for managing coding-agent skills: fetching them from their Sources into a Library, organising them, and installing them into the directories that coding agents read from.

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
The tool's own authoritative store of skills plus their metadata (collections, groups, installations). The single source of truth; the folders of Installations in agent directories are copies of it.
_Avoid_: Store, database, cache, registry / 存储、数据库、缓存、注册表

**Source · 来源**:
The origin a skill was fetched from — typically a git repo, or a local directory.
_Avoid_: Origin, remote, provider / 源头、远端、提供方

**Preview · 预览**:
A temporary look at the skills a Source offers, remote or local, pinned to what the Source contained at that moment; nothing enters the Library. New skills are fetched only from a Preview, exactly as previewed; a successful Fetch consumes it, and an unused one expires.
_Avoid_: Staging, session / 暂存、会话

**Candidate · 候选**:
A skill found in a Preview that can be chosen for Fetch. Folders that look like skills but are invalid are reported alongside, not as Candidates.
_Avoid_: Result, match / 结果、匹配项

**Fetch · 入库**:
Bringing chosen Candidates from a Preview into the Library. Distinct from Install: Fetch brings a skill *into* the tool; Install places it *out* into an Agent's directory.
_Avoid_: Install, download, add, import, pull / 安装、下载、添加、导入、拉取

**Update · 更新**:
Bringing a newer version of an already-fetched skill from its Source into the Library, without a Preview. Installations are left as they are; the user chooses when to Reinstall each one.
_Avoid_: Upgrade, re-fetch / 升级、重新入库

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
A project directory the tool has been told to manage, kept in a list in the Library so project-level Scopes are first-class and installations across projects are visible in one place.
_Avoid_: Workspace, folder, repo / 工作区、文件夹、仓库

**Install · 安装**:
A choice to have one skill from the Library available in a Scope, which places it there as an Installation; **Uninstall · 卸载** withdraws the choice. Installing a skill already installed in that Scope only records the choice — it never overwrites the folder.
_Avoid_: Activate, enable, deploy / 激活、启用、部署

**Group install · 组安装**:
A standing choice to have every member of a Group, current and future, available in a Scope. Uninstalling the Group removes only the Installations that nothing else backs.
_Avoid_: Group activation / 组激活

**Installation · 安装项**:
A skill's folder in the directory the Agent currently defines for a Scope, together with the Library's record of it; users see it as "installed". It exists while an Install or a Group install backs it and is removed with the last one. Folders left in a former directory after a layout change are stale.
_Avoid_: Projection, copy, deployment / 投影、副本、部署

**Install plan · 安装计划**:
A read-only report of what an Install or Group install would do in a Scope, including folders already in the way.
_Avoid_: Preview, dry run / 预览、试运行

**Reinstall · 重新安装**:
Bringing an existing Installation's folder back in line with a version of the skill in the Library, without changing what backs it. The only operation that overwrites an existing Installation on disk.
_Avoid_: Rewrite, refresh, restore, resync / 重写、刷新、还原、重新同步

**Sync · 同步**:
Manual, non-realtime push/pull of the whole Library to and from S3, so a full setup can be reproduced on another machine. Never automatic. Machine-specific paths (Registered projects) are carried as advisory values and may be remapped per machine.
_Avoid_: Backup, replication, upload / 备份、复制、上传

**Sync plan · 同步计划**:
A read-only report, before a push or pull, of which remote copy of the Library it would overwrite or restore from; the push or pull then goes ahead only if the remote is still the one the user confirmed.
_Avoid_: Preview, dry run / 预览、试运行
