# Agent Skills 背景：格式与客户端发现

本文整理 Agent Skills 的开放格式，以及 Claude Code、Codex 如何发现、加载和分发技能。依据 `agent-skills-discovery.md`（2026-09-12 调研，2026-09-14 纳入项目）；客户端行为按该次调研的官方文档快照记录，保留其中未核验源码和未获文档确认的边界。

这里是**生态事实**。项目术语见 [CONTEXT.md](../../CONTEXT.md)，本项目与这些事实的对应关系见文末[与本项目的对应](#与本项目的对应)。规范要求、客户端行为和本项目已实现的校验分别看待。

## Skill 的单位与文件格式

**Agent Skill** 是一种轻量开放格式：一个根目录含 `SKILL.md` 的文件夹，以元数据、任务说明和可选资源封装专项知识与工作流，供 Agent 按需加载。一个 skills 目录可以并列存放多个这样的文件夹；每个文件夹是独立的 Skill。Skill 本身不是插件二进制或 MCP 服务，但可以随插件分发。[Agent Skills Overview](https://agentskills.io/home)

最小形态只有 `my-skill/SKILL.md`。较完整的形态如下；`scripts/`、`references/`、`assets/` 是推荐惯例，也可以有其他文件和子目录：

```text
my-skill/
├── SKILL.md
├── scripts/
├── references/
├── assets/
└── examples/
```

### SKILL.md

文件以 YAML frontmatter 开始，之后是 Markdown 正文。正文没有强制章节结构。

| 字段 | 开放规范的要求 |
| --- | --- |
| `name` | 必填，1–64 个字符；仅小写 `a-z`、数字和连字符，不能首尾或连续使用连字符；必须与父目录名一致 |
| `description` | 必填，1–1024 个非空字符；说明做什么、何时使用 |
| `license` | 可选 |
| `compatibility` | 可选，最多 500 字符 |
| `metadata` | 可选，字符串到字符串的映射 |
| `allowed-tools` | 实验性，空格分隔；支持情况取决于客户端 |

以上字段依据 [Agent Skills Specification](https://agentskills.io/specification) 和[规范源码](https://raw.githubusercontent.com/agentskills/agentskills/main/docs/specification.mdx)。该次调研所见规范没有声明编号版本。

```markdown
---
name: my-skill
description: What it does and when to use it. Agents match tasks against this text.
---

# Instructions

Step-by-step guidance the agent follows once the skill body is loaded.
```

客户端还可以添加自己的 frontmatter 或配套配置文件；这些扩展的运行语义不自动成为跨客户端规范。

## 渐进披露与调用

加载分为三个阶段：

1. **发现（Discovery）**：先读取各技能的 `name` 与 `description` 等目录元数据，规范估计每个技能约 100 tokens。
2. **加载（规范称 Activation）**：任务匹配描述，或用户显式调用时，读取完整 `SKILL.md`。规范建议正文少于 5000 tokens、主文件少于 500 行；这是建议，不是文件格式的硬上限。
3. **执行（Execution）**：按需读取参考资料、脚本和资源。规范没有规定任务与 `description` 的匹配算法。

以上是 [Agent Skills Specification](https://agentskills.io/specification) 描述的渐进披露机制。

Claude Code 在会话开始时把可由模型调用的技能名称与描述放入上下文，调用时再加载正文；这与始终在场的 `CLAUDE.md` 不同。[Extend Claude Code](https://code.claude.com/docs/en/features-overview)

Codex 的初始技能列表还包含文件路径。该次调研的文档给出列表预算为上下文窗口的 2%，窗口未知时上限为 8000 字符；这是客户端文档中的预算，不是 Agent Skills 格式限制。[Build skills](https://learn.chatgpt.com/docs/build-skills)、[Markdown 版本](https://learn.chatgpt.com/codex/build-skills.md)

## Claude Code：发现位置与生效范围

项目技能从启动目录的 `.claude/skills/` 向上找到仓库根。启动目录之下的嵌套技能，在首次读取或编辑相应子目录文件时加载；`--add-dir` 指定目录下的 `.claude/skills/` 也会被扫描。

| 层级 | 路径 | 生效范围 |
| --- | --- | --- |
| 企业 | 托管设置目录内 `.claude/skills/<skill-name>/SKILL.md` | 组织部署到的机器上的所有用户 |
| 个人 | `~/.claude/skills/<skill-name>/SKILL.md` | 本机所有本地项目，不含 Cowork 或云端会话 |
| 项目 | `<repo>/.claude/skills/<skill-name>/SKILL.md` | 仓库中的会话，提交后可供团队共享 |
| 嵌套 | `<subdir>/.claude/skills/<skill-name>/SKILL.md` | 对应子目录，触及其文件时加载 |
| 附加目录 | `--add-dir` 路径下的 `.claude/skills/` | 该附加目录 |
| 插件 | `<plugin>/skills/<skill-name>/SKILL.md` | 插件启用处，使用 `/plugin-name:skill-name` 命名空间 |

发现位置依据 [Extend Claude with skills](https://code.claude.com/docs/en/skills) 和 [Where skills live](https://code.claude.com/docs/en/skills#where-skills-live)。

### 调用与同名处理

默认用户可以输入 `/skill-name`，Claude 也可以按描述自动调用。[Extend Claude with skills](https://code.claude.com/docs/en/skills)

| Frontmatter | 行为 |
| --- | --- |
| `disable-model-invocation: true` | 描述不进入上下文，禁止模型自行调用；用户手动调用时才加载正文 |
| `user-invocable: false` | 隐藏斜杠命令，描述仍进入上下文，模型仍可调用 |

企业、个人、项目三个层级同名时，**企业 > 个人 > 项目**。技能还会替换同名的捆绑命令（不含别名）和 `.claude/commands/` 文件。存在两种并存情况：项目根与嵌套位置的同名技能同时加载；插件技能通过命名空间与其他位置的同名技能同时加载。因此不能把所有来源概括为“同名只保留一份”。[Resolve skills that share a name](https://code.claude.com/docs/en/skills#resolve-skills-that-share-a-name)

### 分发与企业目录

- 项目技能通过提交 `.claude/skills/` 共享。
- 插件把技能放在自身 `skills/` 中；启用由用户 `~/.claude/settings.json`、项目 `.claude/settings.json`、本地 `.claude/settings.local.json` 或托管设置决定。
- 组织可通过托管设置部署企业技能；管理员通过删除托管目录中的技能文件夹移除它。

企业托管目录依平台而异。该次调研的技能页只给出 Linux 删除示例 `/etc/claude-code/.claude/skills/<skill-name>/`，没有枚举各操作系统的托管路径；不能把此路径外推到 macOS 或 Windows。[Share skills](https://code.claude.com/docs/en/skills#share-skills)

## Codex：发现位置与生效范围

该次调研的官方发现表列出以下位置，并说明支持跟随指向技能文件夹的符号链接：

| 层级 | 路径 | 生效范围 |
| --- | --- | --- |
| REPO | `$CWD/.agents/skills`，沿父目录向上直到 `$REPO_ROOT/.agents/skills` | 当前仓库，取决于启动位置 |
| USER | `$HOME/.agents/skills` | 本机任意仓库的个人技能 |
| ADMIN | `/etc/codex/skills` | 整机或容器级默认 |
| SYSTEM | 随 Codex 捆绑的技能 | 启动时对所有人可用 |

发现位置依据 [Build skills](https://learn.chatgpt.com/docs/build-skills) 和 [Where to save skills](https://learn.chatgpt.com/docs/build-skills#where-to-save-skills)。

**文档表使用 `.agents/skills`。** 它没有把 `~/.codex/skills` 或仓库 `.codex/skills` 列为发现根。原调研同时记录了捆绑安装技能仍使用 `$CODEX_HOME/skills`（默认 `~/.codex/skills`）、SYSTEM 缓存位于其 `.system` 子目录的线索；这些线索不足以确认旧个人路径或项目路径仍被扫描，也不足以断言它们已经失效。涉及兼容或迁移时，需要按目标客户端版本另行核验。

### 调用与同名处理

| 方式 | 行为 |
| --- | --- |
| 显式 | 在提示中指明技能，或在 CLI/IDE 中通过 `/skills`、`$skill-name` 选择 |
| 隐式 | 模型按任务与 `description` 的匹配选用 |

`agents/openai.yaml` 中的 `policy.allow_implicit_invocation: false` 关闭隐式调用，仍允许显式 `$skill` 调用。[Build skills](https://learn.chatgpt.com/docs/build-skills)

同 `name` 的技能**不合并**，选择器中可以出现两份同名技能。该次调研的文档没有定义 REPO、USER、ADMIN、SYSTEM、插件之间的同名优先级，不能套用 Claude Code 的覆盖规则。[Where to save skills](https://learn.chatgpt.com/docs/build-skills#where-to-save-skills)

### 分发与未确认的行为

本地技能通过发现目录提供；跨仓库分发，或同时附带 connectors/MCP 时，文档推荐使用插件。ADMIN 表示整机共享，SYSTEM 表示捆绑技能。[Where to save skills](https://learn.chatgpt.com/docs/build-skills#where-to-save-skills)

原调研仍有两处未获所查文档确认的行为：

- 插件技能的调用命名空间，以及它与文件系统技能同名时的处理。
- CWD 之下嵌套技能的加载。文档只描述从 CWD 向上发现，未描述 Claude Code 那样“触及子目录文件时加载”的规则。

## 全局与项目级的区别

二者使用相同的 Skill 文件格式，区别在**发现路径、生效范围和同名处理**。日常所说的“全局技能”通常是当前用户跨项目可用的个人技能；企业、ADMIN、SYSTEM 还有各自的部署范围，不能仅凭“全局”一词视为同一层级。

| 比较项 | Claude Code | Codex |
| --- | --- | --- |
| 个人发现根 | `~/.claude/skills` | `$HOME/.agents/skills` |
| 项目发现根 | `.claude/skills`，另有嵌套与 `--add-dir` 规则 | 从 CWD 到仓库根的 `.agents/skills` |
| 同名处理 | 企业 > 个人 > 项目；嵌套与插件可并存 | 不合并；所查文档未给出跨层级优先级 |
| 关闭模型自动调用 | `disable-model-invocation: true` | `policy.allow_implicit_invocation: false` |
| 用户显式调用 | `/skill-name` | `/skills`、`$skill-name` |

在这些客户端的本地文件流程中，通常无需在对话中逐个注册技能：把完整文件夹放到客户端发现的位置，再由客户端的设置和加载规则决定可见性与调用。项目技能可以随仓库共享，个人技能供本机跨项目使用。目录存在、被发现、正文加载、实际执行是不同阶段。[Claude Code skills](https://code.claude.com/docs/en/skills)、[Codex Build skills](https://learn.chatgpt.com/docs/build-skills)

## 与本项目的对应

库只管理技能内容和安装记录；发现、选用和执行技能由 Agent 负责（见 [ADR 0001](../adr/0001-library-data-in-one-database.md)）。随客户端变化的规则都记在本文，不写成库的通用规则。

- **保存完整的技能文件夹。** `scripts/`、`references/`、`assets/` 等资源都属于技能。渐进披露约束的是 Agent 什么时候读取内容，不意味着库只需保存 `SKILL.md`。客户端扩展字段和配套配置作为文件内容原样保留，它们的含义由客户端解释。
- **入库、安装和 Agent 加载是三件事。** 生态里的“安装”指把文件夹放进 Agent 读取的目录，本项目的安装与之一致；放进库叫入库。规范里的 Activation 指 Agent 加载正文，和本项目的任何操作都无关。安装成功只说明文件已放好，不能证明 Agent 已经发现、选用或执行了它。
- **作用域只覆盖个人全局和已登记项目。** 这里的“全局”指当前用户跨项目，不包括企业、ADMIN 或 SYSTEM 层级。从 CWD 向上查找、嵌套加载、附加目录、插件启用等规则由客户端负责；登记一个项目，并不等于复现了这些规则。
- **安装时的目标冲突，和客户端对同名技能的选择，是两回事。** 安装冲突指要写入的目录已经被占用；同名技能在会话里谁生效，由客户端的优先级决定（见上文各客户端的“调用与同名处理”）。库里有安装记录，不代表该技能在会话中胜出，库也不会因此移除其他作用域里的同名技能。

### 尚未确认或尚未实现的地方

- **Codex 的旧目录 `.codex/skills`。** 库现在装到 `.agents/skills`，以前装到旧目录的文件夹不会被搬走或删除。旧目录是否仍会被当前的 Codex 版本读取，尚未确认。
- **候选校验不是完整的规范校验。** 入库预览时会检查 YAML、名称是否安全，以及 `description` 是否为必填字符串，但不要求名称等于父目录名。上文列出的必填项、长度和命名约束是开放规范的要求，不能据此认为库已经完整执行了这些校验。
