# Mechbox

A tool for managing coding-agent skills: fetching them from their Sources into a Library, organising them, and installing them into the directories that coding agents read from.

Skills here are **[Agent Skills](https://agentskills.io)** — folders with a `SKILL.md` that agents discover from disk and load on demand. For format, discovery scopes, invocation, and same-name behavior, see [ecosystem background](docs/agents/agent-skills-background.md). Product terms stay in this file; how the product maps onto the ecosystem, and known gaps, are at the end of the ecosystem background.

> Term headings are bilingual: the **English** name is the canonical identifier used in code and also the term used in the English UI; the **中文** name is the term used in the Chinese UI.

## Language · 术语

**Skill · 技能**:
A package of instructions and optional resources with a root `SKILL.md`, which a coding agent discovers from disk and loads on demand.
_Avoid_: Plugin, extension, prompt / 插件、扩展、提示词

**Note · 备注**:
A user's own remark on a skill in the Library, shown in the skill's details and when hovering its name. It lives only in the Library: it is not written into the skill's files and does not make a new Version.
_Avoid_: Comment, memo / 评论、注释

**Agent · 智能体**:
A coding agent with a stable code and a display name, loading skills from its global and Registered project directories; changing its name does not change its identity. Built-in Agents (Claude Code, Codex) have directories the product maintains; a user may add a custom Agent and set its directories, the global one under the home directory. Several Agents may read the same directory.
_Avoid_: Assistant, tool, client / 助手、工具、客户端

**Library · 库**:
The tool's own authoritative store of skills plus their metadata (collections, groups, which skills are installed in which Scopes). The single source of truth: installed folders are written from it, and edits made to them reach it only when the user brings them in with an Update.
_Avoid_: Store, database, cache, registry / 存储、数据库、缓存、注册表

**Source · 来源**:
The origin a skill was fetched from — typically a git repo, or a local directory.
_Avoid_: Origin, remote, provider, upstream / 源头、远端、提供方、上游

**Revision · 来源版本**:
What a Source contained at one moment: a commit for a git repo, a content hash otherwise. A Source moving to a new Revision gives a skill a new Version only if that skill's files changed.
_Avoid_: Version, release / 版本、发行版

**Preview · 入库预览**:
A temporary look at the skills a Source offers, remote or local, pinned to what the Source contained at that moment; nothing enters the Library. Skills are fetched from a Preview exactly as previewed; a successful Fetch consumes it, and an unused one expires.
_Avoid_: Staging, session / 暂存、会话

**Candidate · 候选**:
A skill found in a Preview that can be chosen for Fetch. Folders that look like skills but are invalid are reported alongside, not as Candidates.
_Avoid_: Result, match / 结果、匹配项

**Fetch · 入库**:
Bringing skills into the Library: chosen Candidates from a Preview, or Unmanaged skills in a Scope that the Library did not install. Distinct from Install: Fetch brings a skill *into* the tool; Install places it *out* into an Agent's directory. Fetching an Unmanaged skill leaves its folder as it is and records it as installed in that Scope.
_Avoid_: Install, download, add, import, pull, adopt / 安装、下载、添加、导入、拉取、收进库

**Version · 版本**:
A complete copy of one skill's files as a Fetch or an Update brought them into the Library, from its Source or from its folder in a Scope. Each skill has exactly one current Version; Install and Reinstall place that one.
_Avoid_: Revision, snapshot, release / 来源版本、快照、发行版

**Update · 更新**:
Giving skills already in the Library a new Version, without a Preview: the skills the user kept checked in an Update plan take their Source's newest Revision, or one skill takes the edits the user made to its folder in a Scope. New skills never enter this way, only by Fetch. The same confirmation Reinstalls them in the Scopes the user kept checked; the rest are left as they are.
_Avoid_: Upgrade, re-fetch / 升级、重新入库

**Update check · 检查更新**:
A read-only look at whether a Collection's Source has moved to a new Revision, and what that would change. It never changes the Library or installed folders, so it may run without the user asking.
_Avoid_: Refresh, poll, sync / 刷新、轮询、同步

**Update plan · 更新计划**:
A read-only report, before an Update, of how a Collection's skills differ between the Library and the Source's newest Revision, including skills the Source has removed or probably renamed, and of the Scopes where the changed skills are installed. The user chooses which skills and which Scopes the Update takes.
_Avoid_: Preview, report, diff / 预览、报告、差异

**Skip · 跳过**:
The user's choice to set aside a skill's change in one Revision of its Source, so it no longer awaits an Update until a later Revision changes that skill again. Leaving a skill unchecked in an Update plan is not a Skip.
_Avoid_: Ignore, dismiss, pin / 忽略、不再提醒、固定

**Rollback · 回滚**:
Returning the skills an Update changed to the Versions they had before it, and Reinstalling them in the Scopes that Update reinstalled, except where they have since been reinstalled or edited.
_Avoid_: Undo, revert, restore, downgrade / 撤销、回退、还原、降级

**Collection · 集合**:
Skills grouped by their Source (i.e. by source repo). Automatic — a skill's collection follows from where it came.
_Avoid_: Repo group, bundle / 仓库分组、捆绑包

**Group · 组**:
A user-defined gathering of skills by scenario (e.g. "frontend debugging"). Orthogonal to Collection.
_Avoid_: Tag, category, set / 标签、分类、集

**Scope · 作用域**:
One of two kinds: an Agent's global level, or an Agent together with one Registered project. Chosen by kind, never by directory. A skill installed in a Scope has its folder in the Agent's directory for that Scope; users see, inspect, reinstall and uninstall it one skill and one Scope at a time. Scopes of Agents that read the same directory share one folder: installing in one counts for all of them, and uninstalling from one removes it from all.
_Avoid_: Target, context, location, path, Installation / 目标、上下文、位置、路径、安装项

**Registered project · 已登记项目**:
A project directory the tool has been told to manage, kept in a list in the Library so project-level Scopes are first-class and what is installed across projects is visible in one place.
_Avoid_: Workspace, folder, repo / 工作区、文件夹、仓库

**Unmanaged skill · 未纳管技能**:
A skill folder in a Scope's directory that the Library does not account for: either the Library did not install it — placed by another tool, cloned, copied or written by hand, or committed in a Registered project's repository — or it is installed but edited since. The tool never changes or removes one it did not install, and Fetch brings it under management; an edited installed one stays installed, and an Update brings its edits in.
_Avoid_: External skill, orphan, untracked, unhosted / 外部技能、不是从这里装的技能、孤儿、未跟踪、未托管

**Install · 安装**:
A choice to have one skill from the Library available in a Scope, which places its folder there; **Uninstall · 卸载** withdraws the choice. A skill stays installed in a Scope while a direct Install or a Group install keeps it there, and its folder is removed with the last one. Installing a skill already installed in that Scope, or Fetching an Unmanaged skill there, only records the choice — it never overwrites the folder.
_Avoid_: Activate, enable, deploy, projection / 激活、启用、部署、投影

**Group install · 组安装**:
A standing choice to have every member of a Group, current and future, available in a Scope. Uninstalling the Group removes a member from that Scope only if no direct Install or other Group install keeps it there.
_Avoid_: Group activation / 组激活

**Install plan · 安装计划**:
A read-only report of what an Install or Group install would do in a Scope, including folders already in the way.
_Avoid_: Preview, dry run / 预览、试运行

**Reinstall · 重新安装**:
Bringing the folder of a skill installed in a Scope back in line with a version of that skill in the Library, without changing its direct Install or Group installs. The only operation that overwrites an installed folder.
_Avoid_: Rewrite, refresh, restore, resync / 重写、刷新、还原、重新同步

**Sync · 同步**:
Manual, non-realtime push/pull of the whole Library to and from S3, so a full setup can be reproduced on another machine. Never automatic. After pulling on another machine, the user points each Registered project to where it lives on that machine. In Chinese, push is 推送 and pull is 拉回.
_Avoid_: Backup, replication, upload / 备份、复制、上传

**Sync plan · 同步计划**:
A read-only report, before a push or pull, of which Library on the remote it would overwrite or restore from; the push or pull then goes ahead only if the remote is still the one the user confirmed.
_Avoid_: Preview, dry run / 预览、试运行
