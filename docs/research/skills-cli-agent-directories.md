# skills CLI 对各 coding agent 的目录分配

**Date:** 2026-11-17  
**Question:** vercel-labs/skills 给每个 coding agent 分配的项目级、全局级技能目录是什么？仓库里有没有各 agent 的图标？  
**Method:** 读本地克隆 `/Users/dur/code/github-clone/vercel-labs/skills`，以 `src/agents.ts` 的 `agents` 为目录表，用 `src/installer.ts` 的 `getAgentBaseDir` / `installSkillForAgent` 核对「声明的目录」和「一次新安装真正写入的目录」。`docs/agents.md` 的表由 `scripts/sync-agents.ts` 从同一对象生成。图标用全库文件名搜索（排除 `node_modules` 与 `.git`）。  
**Snapshot:** `main` @ `57217cf86ba38148a57af9e90d14a4c049558330`（2026-11-17，`refactor: upgrade nostics (#2765)`），npm 包 `skills@1.9.0`。工作区干净，跟踪 `origin/main`。共 **79** 个 agent id，含手动目标 `universal`。

路径写成 `~` 表示进程的 home。`$XDG_CONFIG_HOME` 未设置时，下面写「默认 `~/.config`」的位置就是 `~/.config`。这些值在进程启动、模块加载时算一次。

---

## 结论

每个已支持的 agent 有两个声明字段：

| 字段 | 含义 |
| --- | --- |
| `skillsDir` | 相对当前项目的技能目录 |
| `globalSkillsDir` | 全局技能目录的绝对路径；`undefined` 表示不做全局安装 |

另外有一套**规范目录**，跟具体 agent 无关：

| 层级 | 路径 |
| --- | --- |
| 项目 | `<项目>/.agents/skills/<技能名>/` |
| 全局 | `~/.agents/skills/<技能名>/` |

常量在 `src/constants.ts`：`AGENTS_DIR = '.agents'`，`SKILLS_SUBDIR = 'skills'`。`getCanonicalSkillsDir` 用它们拼路径。

`docs/agents.md` 的 Global Path 列印的是 `globalSkillsDir`。对「项目用 `.agents/skills`、并且全局也读 `~/.agents/skills`」的 agent，一次新的全局安装**写规范目录**。声明的 `globalSkillsDir` 仍会被 `list` / `remove` 扫到，用来收拾旧版本留在 agent 自己目录里的技能。GitHub Copilot 有对应用例：全局安装后存在 `~/.agents/skills/probe-skill`，不存在 `~/.copilot/skills/probe-skill`（`tests/antigravity-global-install.test.ts`）。

默认安装模式是 `symlink`：实体在规范目录，需要单独目录的 agent 再用符号链接指过去。Windows 上链接类型是 junction。`copy` 模式不写规范目录，直接复制到 `getAgentBaseDir` 返回的那个目录。

---

## 一次 symlink 安装写到哪里

记一个 agent 为 **universal**：`skillsDir === '.agents/skills'`。  
记它 **全局读规范目录**：它是 universal，且没有 `globalReadsCanonical: false`。只有 Antigravity 和 Antigravity CLI 把这个标志设成 `false`。

**项目级**

- universal：只写 `<项目>/.agents/skills/<技能名>/`。
- 其他 agent：实体写到同一规范目录，再把 `<项目>/<skillsDir>/<技能名>` 链到它。
- 项目里还没有该 agent 的根目录时，不创建这条链接，避免无故造出 `.windsurf/`、`.kiro/` 这类目录。根目录是 `skillsDir` 的第一段（Windsurf 看 `.windsurf`，Tabnine CLI 看 `.tabnine`，AstrBot 看 `data`，OpenClaw 看 `skills`）。Claude Code 设置了 `createProjectSkillsDirByDefault`，缺 `.claude` 也会建。用户用 `--agent` 点名时可以强制创建。

**全局**

- `globalSkillsDir` 为空（Eve、PromptScript）：拒绝全局安装。
- 全局读规范目录：只写 `~/.agents/skills/<技能名>/`，不在声明的全局目录里再放一份。
- 其余：实体写 `~/.agents/skills/<技能名>/`，再把 `<globalSkillsDir>/<技能名>` 链过去。Antigravity 全局安装走这条：链接出现在 `~/.gemini/config/skills/`。

Eve 是例外。它的 symlink 安装把规范位置改成自己的 `agent/skills`（或子 agent 目录），不经过 `.agents/skills`。

---

## 项目目录是 `.agents/skills` 的 agent

这些 agent 的项目安装都进 `<项目>/.agents/skills/`。  
「全局安装」列是 symlink 模式下新安装的落点。

| `--agent` | 名称 | 声明的全局目录 | 全局安装 | 认定为已安装 |
| --- | --- | --- | --- | --- |
| `amp` | Amp | `$XDG_CONFIG_HOME/agents/skills` | 只写 `~/.agents/skills` | `$XDG_CONFIG_HOME/amp` |
| `antigravity` | Antigravity | `~/.gemini/config/skills` | 规范目录 + 链接到声明目录 | `~/.gemini/antigravity` |
| `antigravity-cli` | Antigravity CLI | `~/.gemini/config/skills` | 同上，与 Antigravity 共用 | `~/.gemini/antigravity-cli` |
| `cline` | Cline | `~/.agents/skills` | 只写 `~/.agents/skills` | `~/.cline` |
| `codex` | Codex | `~/.agents/skills` | 只写 `~/.agents/skills` | `$CODEX_HOME`（默认 `~/.codex`），或 `/etc/codex` |
| `cursor` | Cursor | `~/.cursor/skills` | 只写 `~/.agents/skills` | `~/.cursor` |
| `deepagents` | Deep Agents | `~/.deepagents/agent/skills` | 只写 `~/.agents/skills` | `~/.deepagents` |
| `dexto` | Dexto | `~/.agents/skills` | 只写 `~/.agents/skills` | `~/.dexto` |
| `droid` | Droid | `~/.factory/skills` | 只写 `~/.agents/skills` | `~/.factory` |
| `firebender` | Firebender | `~/.firebender/skills` | 只写 `~/.agents/skills` | `~/.firebender` |
| `fx` | fx | `~/.agents/skills` | 只写 `~/.agents/skills` | `~/.fx` |
| `gemini-cli` | Gemini CLI | `~/.gemini/skills` | 只写 `~/.agents/skills` | `~/.gemini` |
| `github-copilot` | GitHub Copilot | `~/.copilot/skills` | 只写 `~/.agents/skills` | `~/.copilot` |
| `kilo` | Kilo Code | `~/.kilo/skills` | 只写 `~/.agents/skills` | `~/.kilo` 或 `~/.kilocode` |
| `kimi-code-cli` | Kimi Code CLI | `~/.agents/skills` | 只写 `~/.agents/skills` | `~/.kimi-code` 或 `~/.kimi` |
| `loaf` | Loaf | `~/.agents/skills` | 只写 `~/.agents/skills` | `~/.loaf` |
| `opencode` | OpenCode | `$XDG_CONFIG_HOME/opencode/skills` | 只写 `~/.agents/skills` | `$XDG_CONFIG_HOME/opencode` |
| `pi` | Pi | `~/.agents/skills` | 只写 `~/.agents/skills` | `~/.pi/agent` |
| `promptscript` | PromptScript | 无 | 不支持 | 项目内 `.promptscript` 或 `promptscript.yaml` |
| `replit` | Replit | `$XDG_CONFIG_HOME/agents/skills` | 只写 `~/.agents/skills` | 项目内 `.replit` |
| `sarvam-code` | Sarvam Code | `~/.agents/skills` | 只写 `~/.agents/skills` | `$SARVAM_HOME`（默认 `~/.sarvam`） |
| `universal` | Universal | `$XDG_CONFIG_HOME/agents/skills` | 只写 `~/.agents/skills` | 从不；只能手动指定 |
| `warp` | Warp | `~/.agents/skills` | 只写 `~/.agents/skills` | `~/.warp` |
| `zed` | Zed | `~/.agents/skills` | 只写 `~/.agents/skills` | `$XDG_CONFIG_HOME/zed`，或 `%APPDATA%/Zed`，或 `$FLATPAK_XDG_CONFIG_HOME/zed` |

源码里几条和目录有关的说明：

- Antigravity（含 CLI）：agy、IDE、Antigravity 2.0 的全局技能在 `~/.gemini/config/skills`，工作区外不读 `~/.agents/skills`。交互安装列表里不把它们放进 universal 那一组。
- Droid 把 `.agents/skills` 和 `~/.agents/skills` 当兼容位置来读，所以新安装放那里。`globalSkillsDir` 仍是 `~/.factory/skills`，`remove` 会清掉旧版写在那里的技能。
- fx 自己还会发现 `.fx/skills`。CLI 安装目标是共享的 `.agents/skills` 与 `~/.agents/skills`，不往 `.fx/skills` 写。
- Pi 从工作目录向上读各级 `.agents/skills/`，全局读 `~/.agents/skills/`。
- Codex 的 `$CODEX_HOME` 只用于探测，不改变技能目录。Sarvam 的 `$SARVAM_HOME` 同样只用于探测。
- Replit 与 Universal 不出现在 universal 名单里（`showInUniversalList: false`）。目录规则仍按 universal 算。Universal 的 `detectInstalled` 恒为 false。

---

## 项目目录是 agent 自己的

symlink 模式下：实体在 `<项目>/.agents/skills/<技能名>/` 与 `~/.agents/skills/<技能名>/`；项目链接到下表的项目目录（根目录已存在，或该 agent 会主动创建根目录）；全局链接到下表的全局目录。Eve 见下节，不走这套规范目录。

| `--agent` | 名称 | 项目目录 | 全局目录 | 认定为已安装 |
| --- | --- | --- | --- | --- |
| `adal` | AdaL | `.adal/skills` | `~/.adal/skills` | `~/.adal` |
| `aider-desk` | AiderDesk | `.aider-desk/skills` | `~/.aider-desk/skills` | `~/.aider-desk` |
| `astrbot` | AstrBot | `data/skills` | `~/.astrbot/data/skills` | 项目内 `data/skills`，或 `~/.astrbot` |
| `augment` | Augment | `.augment/skills` | `~/.augment/skills` | `~/.augment` |
| `autohand-code` | Autohand Code CLI | `.autohand/skills` | `$AUTOHAND_HOME/skills`（默认 `~/.autohand/skills`） | `$AUTOHAND_HOME` |
| `bob` | IBM Bob | `.bob/skills` | `~/.bob/skills` | `~/.bob` |
| `claude-code` | Claude Code | `.claude/skills` | `$CLAUDE_CONFIG_DIR/skills`（默认 `~/.claude/skills`） | `$CLAUDE_CONFIG_DIR`。项目里没有 `.claude` 时也会创建 |
| `codearts-agent` | CodeArts Agent | `.codeartsdoer/skills` | `~/.codeartsdoer/skills` | `~/.codeartsdoer` |
| `codebuddy` | CodeBuddy | `.codebuddy/skills` | `~/.codebuddy/skills` | 项目内 `.codebuddy`，或 `~/.codebuddy` |
| `codemaker` | Codemaker | `.codemaker/skills` | `~/.codemaker/skills` | `~/.codemaker` |
| `codestudio` | Code Studio | `.codestudio/skills` | `~/.codestudio/skills` | `~/.codestudio` |
| `command-code` | Command Code | `.commandcode/skills` | `~/.commandcode/skills` | `~/.commandcode` |
| `continue` | Continue | `.continue/skills` | `~/.continue/skills` | 项目内 `.continue`，或 `~/.continue` |
| `cortex` | Cortex Code | `.cortex/skills` | `~/.snowflake/cortex/skills` | `~/.snowflake/cortex` |
| `crush` | Crush | `.crush/skills` | `~/.config/crush/skills` | `~/.config/crush` |
| `devin` | Devin for Terminal | `.devin/skills` | `$XDG_CONFIG_HOME/devin/skills` | `$XDG_CONFIG_HOME/devin` |
| `eve` | Eve | `agent/skills` | 无 | 项目内有 `agent/`，且 `package.json` 依赖 `eve` |
| `forgecode` | ForgeCode | `.forge/skills` | `~/.forge/skills` | `~/.forge` |
| `goose` | Goose | `.goose/skills` | `$XDG_CONFIG_HOME/goose/skills` | `$XDG_CONFIG_HOME/goose` |
| `grok` | Grok Build | `.grok/skills` | `$GROK_HOME/skills`（默认 `~/.grok/skills`） | `$GROK_HOME` |
| `hermes-agent` | Hermes Agent | `.hermes/skills` | `$HERMES_HOME/skills`（默认 `~/.hermes/skills`） | `$HERMES_HOME` |
| `iflow-cli` | iFlow CLI | `.iflow/skills` | `~/.iflow/skills` | `~/.iflow` |
| `inference-sh` | inference.sh | `.inferencesh/skills` | `~/.inferencesh/skills` | `~/.inferencesh` |
| `jazz` | Jazz | `.jazz/skills` | `~/.jazz/skills` | `~/.jazz`，或项目内 `.jazz` |
| `junie` | Junie | `.junie/skills` | `~/.junie/skills` | `~/.junie` |
| `kimchi` | Kimchi | `.kimchi/skills` | `~/.config/kimchi/harness/skills` | `~/.config/kimchi` |
| `kiro-cli` | Kiro CLI | `.kiro/skills` | `~/.kiro/skills` | `~/.kiro` |
| `kode` | Kode | `.kode/skills` | `~/.kode/skills` | `~/.kode` |
| `lingma` | Lingma | `.lingma/skills` | `~/.lingma/skills` | `~/.lingma` |
| `mcpjam` | MCPJam | `.mcpjam/skills` | `~/.mcpjam/skills` | `~/.mcpjam` |
| `minimax-code` | MiniMax Code | `.minimax/skills` | `~/.minimax/skills` | `~/.minimax`，或 `/Applications/MiniMax Code.app` |
| `mistral-vibe` | Mistral Vibe | `.vibe/skills` | `$VIBE_HOME/skills`（默认 `~/.vibe/skills`） | `$VIBE_HOME` |
| `moxby` | Moxby | `.moxby/skills` | `~/.moxby/skills` | `~/.moxby` |
| `mux` | Mux | `.mux/skills` | `~/.mux/skills` | `~/.mux` |
| `neovate` | Neovate | `.neovate/skills` | `~/.neovate/skills` | `~/.neovate` |
| `ona` | Ona | `.ona/skills` | `~/.ona/skills` | `~/.ona` |
| `openclaw` | OpenClaw | `skills` | 见下 | `~/.openclaw`、`~/.clawdbot`、`~/.moltbot` 任一存在 |
| `openhands` | OpenHands | `.openhands/skills` | `~/.openhands/skills` | `~/.openhands` |
| `pochi` | Pochi | `.pochi/skills` | `~/.pochi/skills` | `~/.pochi` |
| `posit-assistant` | Posit Assistant | `.posit/assistant/skills` | `~/.posit/assistant/skills` | `~/.posit/assistant`，或旧目录 `~/.positai` |
| `qoder` | Qoder | `.qoder/skills` | `~/.qoder/skills` | `~/.qoder` |
| `qoder-cn` | Qoder CN | `.qoder/skills` | `~/.qoder-cn/skills` | `~/.qoder-cn` |
| `qwen-code` | Qwen Code | `.qwen/skills` | `~/.qwen/skills` | `~/.qwen` |
| `reasonix` | Reasonix | `.reasonix/skills` | `$REASONIX_HOME/skills` | `$REASONIX_HOME` |
| `roo` | Roo Code | `.roo/skills` | `~/.roo/skills` | `~/.roo` |
| `rovodev` | Rovo Dev | `.rovodev/skills` | `~/.rovodev/skills` | `~/.rovodev` |
| `tabnine-cli` | Tabnine CLI | `.tabnine/agent/skills` | `~/.tabnine/agent/skills` | `~/.tabnine` |
| `terramind` | Terramind | `.terramind/skills` | `~/.terramind/skills` | `~/.terramind` |
| `tinycloud` | Tinycloud | `.tinycloud/skills` | `~/.tinycloud/skills` | `~/.tinycloud` |
| `trae` | Trae | `.trae/skills` | `~/.trae/skills` | `~/.trae` |
| `trae-cn` | Trae CN | `.trae/skills` | `~/.trae-cn/skills` | `~/.trae-cn` |
| `windsurf` | Windsurf | `.windsurf/skills` | `~/.codeium/windsurf/skills` | `~/.codeium/windsurf` |
| `zcode` | ZCode | `.zcode/skills` | `~/.zcode/skills` | `~/.zcode`，或 `/Applications/ZCode.app` |
| `zencoder` | Zencoder | `.zencoder/skills` | `~/.zencoder/skills` | `~/.zencoder` |
| `zenflow` | Zenflow | `.zencoder/skills` | `~/.zencoder/skills` | `~/.zencoder`（与 Zencoder 相同） |

### 环境变量与写死的 `.config`

跟着 `$XDG_CONFIG_HOME`（未设置则为 `~/.config`）走的，是 Amp、OpenCode、Replit、Universal、Devin、Goose，以及 Zed 的探测。测试要求这条路径在 macOS 上也不要变成 `~/Library/Preferences`，在 Windows 上也不要变成 `%APPDATA%`（`tests/xdg-config-paths.test.ts`）。实现用的是 `xdg-basedir`，不是 `env-paths`。

下面几个**不**读 `$XDG_CONFIG_HOME`，路径里的 `.config` 是写死的：

| Agent | 全局技能目录 |
| --- | --- |
| Crush | `~/.config/crush/skills` |
| Kimchi | `~/.config/kimchi/harness/skills` |

Reasonix 未设置 `$REASONIX_HOME` 时：Windows 用 `%APPDATA%/reasonix/skills`（没有 `APPDATA` 则是 `~/AppData/Roaming/reasonix/skills`），其他系统用 `~/.reasonix/skills`。

OpenClaw 的全局目录在加载时按磁盘现状选一次，顺序固定（`getOpenClawGlobalSkillsDir`）：

1. `~/.openclaw` 存在 → `~/.openclaw/skills`
2. 否则 `~/.clawdbot` 存在 → `~/.clawdbot/skills`
3. 否则 `~/.moltbot` 存在 → `~/.moltbot/skills`
4. 否则仍用 `~/.openclaw/skills`

项目目录固定是仓库根上的 `skills/`，不是 `.openclaw/skills`。

### Eve

- 根 agent：`<项目>/agent/skills/`。没有全局目录。
- 子 agent：`<项目>/agent/subagents/<名字>/skills/`。只在项目级扫描。symlink 安装时，这个目录本身就是实体所在，不再链到 `.agents/skills`。

### 目录内部的一层

Hermes 允许 `~/.hermes/skills/<分类>/<技能>/SKILL.md` 这种中间分类目录（没有自己的 `SKILL.md`、下面有带 `SKILL.md` 的子目录）。安装若会覆盖这样一个分类目录就跳过。这是技能目录里的布局，不是另一个 agent 根。

---

## 发现技能来源时额外看的项目目录

入库前的目录扫描，除了各 agent 当前的 `skillsDir`，还包含这些**旧的项目路径**（agent 改去 `.agents/skills` 之后，已发布的技能仓库仍把技能放在这里）。它们不是安装目标。

```text
.cline/skills
.codex/skills
.factory/skills
.github/skills
.kilo/skills
.kilocode/skills
.opencode/skills
.pi/skills
```

同一段发现逻辑里的通用位置（`scripts/sync-agents.ts` 的 `generateSkillDiscoveryPaths`）：仓库根若有 `SKILL.md`、`skills/`、`skills/.curated/`、`skills/.experimental/`、`skills/.system/`。根上的 `skills/` 与 OpenClaw 的项目目录是同一个相对路径。

---

## 记录文件（不是技能目录）

| 层级 | 路径 |
| --- | --- |
| 项目 | `<项目>/skills-lock.json` |
| 全局 | `$XDG_STATE_HOME/skills/.skill-lock.json`；未设置则为 `~/.agents/.skill-lock.json` |

---

## 图标

这个快照里**没有**各 agent 的图标。全库没有 `.svg`、`.png`、`.ico`、`.webp`（不含依赖目录与 `.git`）。公开的 `AgentInfo`（`src/api/agents.ts`）只有 `id`、`name`、`projectSkillsDir`、`globalSkillsDir`、`universal`。

仓库里和图形有关的只有两处，都不是 agent 图标：

- `README.md` 引用 shields.io 上的 Vercel Labs / npm / license 徽章。
- `src/color.ts` 里 CLI 自己的终端字符 logo，是一条灰度渐变，全命令共用。

---

## 依据

| 内容 | 文件 |
| --- | --- |
| agent 表、环境变量、OpenClaw / Eve、旧项目目录 | `src/agents.ts` |
| 类型与 `globalReadsCanonical` 的含义 | `src/types.ts` 的 `AgentConfig` |
| 规范目录常量 | `src/constants.ts` |
| 安装落点、缺项目根目录时不建链接、Eve 的规范位置 | `src/installer.ts` 的 `getCanonicalSkillsDir`、`getAgentBaseDir`、`installSkillForAgent`、`shouldSkipProjectAgentSymlink` |
| 全局 Copilot 只写 `~/.agents/skills`；Antigravity 全局写 `~/.gemini/config/skills` | `tests/antigravity-global-install.test.ts` |
| OpenClaw 全局回退顺序 | `tests/openclaw-paths.test.ts` |
| XDG 与 Cursor / Cline 的全局字段 | `tests/xdg-config-paths.test.ts` |
| 对外列出的字段（无图标） | `src/api/types.ts`、`src/api/agents.ts` |
| 文档表的生成方式 | `scripts/sync-agents.ts`，输出在 `docs/agents.md` |
| 全局锁、项目锁 | `src/skill-lock.ts` 的 `getSkillLockPath`，`src/local-lock.ts` |
