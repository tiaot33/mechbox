# TUI language comparison: Go vs Rust vs TypeScript

> **已被取代**:本调研比较的是做 TUI 用哪种语言;之后决定桌面端用 Wails v3、与库在同一个进程里，见 [ADR 0008](../adr/0008-desktop-is-wails-v3-in-process.md)。本文只作为当时的调研记录保留。

**Date:** 2026-07-19  
**Question:** 调研做 TUI（Terminal User Interface）时，Go、Rust、TypeScript 哪个更好？各自用什么框架？  
**Method:** 以各框架官方文档、GitHub README、pkg.go.dev / crates.io / npm 等第一方来源为主；星标、下载量等为时间敏感信号，数值以调研当日可见的官方页面为准。

---

## Summary recommendation

没有「永远更好」的语言。选型应同时看 **团队栈、分发形态、架构偏好、性能与生态成熟度**。

| 场景 | 更推荐 | 主框架 | 理由（摘要） |
|------|--------|--------|--------------|
| 需要单文件静态二进制、跨平台 CLI/TUI、团队熟悉 Go | **Go** | [Bubble Tea](https://github.com/charmbracelet/bubbletea)（+ Bubbles / Lip Gloss / Huh） | Elm 架构清晰；Charm 生态完整；生产案例多 |
| 需要「表单/向导」式交互而非全屏 app | **Go** | [Huh](https://github.com/charmbracelet/huh) | 官方定位 forms/prompts，可嵌入 Bubble Tea |
| 需要复杂 retained 组件（表格/树/表单页）且偏好命令式 widget | **Go** | [tview](https://github.com/rivo/tview)（底层 [tcell](https://github.com/gdamore/tcell)） | 丰富 widget；K9s 等生产项目 |
| 需要最高性能、强类型、immediate-mode 控制、原生二进制 | **Rust** | [Ratatui](https://ratatui.rs/) + [crossterm](https://github.com/crossterm-rs/crossterm) | 官方明确 immediate-mode；工业采用面广 |
| 团队已是 React/前端栈，要做交互式 CLI 进度/表单 | **TypeScript** | [Ink](https://github.com/vadimdemedes/ink) | 「React for CLIs」；Claude Code / Gemini CLI / Wrangler 等在用 |
| 需要 TS 全屏复杂 TUI、可接受 native 依赖 / 新栈 | **TypeScript** | [OpenTUI](https://opentui.com/) | Zig 核心 + TS bindings；React/Solid；OpenCode 生产使用 |
| 遗留 Node 全屏 TUI / DOM 式 widget | **TypeScript** | [blessed](https://github.com/chjj/blessed) 或 [terminal-kit](https://github.com/cronvel/terminal-kit) | 成熟但维护节奏与现代 React 路线不同 |

**一句话：**  
- **交付工具给终端用户、重视二进制与运维友好 → Go 或 Rust**  
- **与前端团队共享组件心智 / 已有 Node 发布渠道 → TypeScript（Ink 优先，重 UI 看 OpenTUI）**  
- **性能极限与系统级工具 → Rust + Ratatui**  
- **快速做出美观可维护的全屏/inline TUI 且团队 Go → Bubble Tea**

---

## Go

### Frameworks

#### 1. Bubble Tea（主流推荐，高层框架）

- **Repo / docs:** [github.com/charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea)、[pkg.go.dev](https://pkg.go.dev/github.com/charmbracelet/bubbletea)、Charm 站点 [charm.land](https://charm.land/)
- **Stars（调研日）:** 约 **43.8k**（[GitHub](https://github.com/charmbracelet/bubbletea)）
- **Latest release 可见:** v2.0.8（2026-07-03，[Releases](https://github.com/charmbracelet/bubbletea/releases)）
- **架构:** **The Elm Architecture** — `Model` + `Init` / `Update` / `View`（[README](https://github.com/charmbracelet/bubbletea)、[pkg.go.dev Overview](https://pkg.go.dev/github.com/charmbracelet/bubbletea)）
- **能力（官方自述）:**
  - 适合 simple 与 complex 终端应用；inline、full-window 或混合（[README](https://github.com/charmbracelet/bubbletea)）
  - 生产已用；high-performance cell-based renderer、color downsampling、declarative views、键盘/鼠标、clipboard 等（[README](https://github.com/charmbracelet/bubbletea)）
  - 官方称 *battle-tested* / *production-ready*（[pkg.go.dev](https://pkg.go.dev/github.com/charmbracelet/bubbletea)）
  - Dependents：README 称 **18,000+** applications（[network/dependents 链接](https://github.com/charmbracelet/bubbletea/network/dependents)）
- **配套生态（Charm 官方）:**
  - [Bubbles](https://github.com/charmbracelet/bubbles) — 通用组件（~8.7k stars）
  - [Lip Gloss](https://github.com/charmbracelet/lipgloss) — 样式与布局（~11.6k stars）
  - [Huh](https://github.com/charmbracelet/huh) — forms & prompts；可 standalone 或作为 `tea.Model` 嵌入（[Huh README](https://github.com/charmbracelet/huh)）
  - Harmonica、Glamour、Wish（SSH apps）等（[charm.land](https://charm.land/)）
- **行业使用示例（Bubble Tea README 列举）:** Azure Aztify、CockroachDB、Trufflehog、NVIDIA container-canary、AWS eks-node-viewer、MinIO mc、Ubuntu Authd 等

#### 2. tcell（底层 terminal 包）

- **Repo:** [github.com/gdamore/tcell](https://github.com/gdamore/tcell)
- **Stars:** 约 **5.2k**
- **定位:** cell-based 终端包（类 termbox，但能力更多）；**pure Go，无 CGO**（[README](https://github.com/gdamore/tcell)）
- **能力:** 富 Unicode、宽字符/grapheme、键盘协议、鼠标、24-bit color、bracketed paste；POSIX / Windows / WASM / Plan 9（best-effort）
- **版本:** v3 有 breaking changes；v3.4.0 于 2026-05-17 发布（[Releases](https://github.com/gdamore/tcell/releases)）

#### 3. tview（retained widgets，基于 tcell）

- **Repo:** [github.com/rivo/tview](https://github.com/rivo/tview)
- **Stars:** 约 **14k**
- **架构:** **retained / widget tree** — `Application` + primitives（Box, Form, Table, TreeView, Flex, Grid, Pages…）（[README](https://github.com/rivo/tview)）
- **依赖:** tcell + uniseg
- **生产示例:** K9s、podman-tui、gdu、gh 相关工具等（README 列表）
- **维护风格:** 强调向后兼容（[README Backwards-Compatibility](https://github.com/rivo/tview)）；SemVer release 如 v0.42.0（2025-08）

#### 4. 语言/运行时注意

- Go **标准库没有**完整 TUI 框架；终端能力需第三方（tcell 或 Bubble Tea 自管 tty）
- Go 并发模型（goroutine + channel）与 Bubble Tea 的 `Cmd`（异步 IO → `Msg`）契合良好（[Cmd 文档](https://pkg.go.dev/github.com/charmbracelet/bubbletea#Cmd)）
- 分发：`go build` 静态/准静态二进制，对 CLI 工具极友好

### Strengths / trade-offs

| 维度 | 评价 |
|------|------|
| 架构清晰度 | Bubble Tea Elm 模型学习曲线友好；tview 更接近传统 GUI widget |
| 生态 | Charm 一体化（样式/组件/表单/SSH）是 Go 侧最大优势 |
| 性能 | 足够多数 dashboard/CLI；极限刷新率场景 Rust 更稳 |
| 类型安全 | 静态类型，但无 Rust 级所有权保证 |
| 分发 | 单二进制优秀 |
| 跨平台 | tcell / Bubble Tea 官方均覆盖主流桌面 OS |
| 开发体验 | 编译快于 Rust；调试 TUI 需 headless delve（Bubble Tea 文档有说明） |

### Sources

- https://github.com/charmbracelet/bubbletea  
- https://pkg.go.dev/github.com/charmbracelet/bubbletea  
- https://charm.land/  
- https://github.com/charmbracelet/bubbles  
- https://github.com/charmbracelet/lipgloss  
- https://github.com/charmbracelet/huh  
- https://github.com/gdamore/tcell  
- https://github.com/rivo/tview  

---

## Rust

### Frameworks

#### 1. Ratatui（主流推荐，tui-rs 继任者）

- **Website / docs:** [ratatui.rs](https://ratatui.rs/)、[docs.rs/ratatui](https://docs.rs/ratatui/latest/ratatui/)、[GitHub](https://github.com/ratatui/ratatui)
- **Stars:** 约 **21.7k–21.8k**（官网与 GitHub 一致量级）
- **Version:** 0.30.2（2026-06-19，[docs.rs / releases](https://github.com/ratatui/ratatui/releases)）
- **生态信号（官网）:** 38.2M crates.io downloads；4800+ reverse dependencies；用户 logo 含 Netflix、OpenAI、xAI、AWS、Vercel、Hugging Face 等（[ratatui.rs](https://ratatui.rs/)）
- **架构:** **Immediate-mode rendering** — 每帧根据应用状态重绘 UI；无持久 widget 对象（[Rendering 概念文档](https://ratatui.rs/concepts/rendering/)）  
  - 对比 retained mode：传统 GUI 创建控件后改属性；immediate mode 每帧「画出」UI  
  - 优点：状态与 UI 同步简单、条件布局灵活  
  - 代价：开发者负责 **render loop + event loop**；框架不替你组织大型应用结构（文档明确说明；提供 [templates](https://github.com/ratatui/templates)）
- **渲染细节（docs.rs）:** intermediate buffers + diff，仅将变更写入终端（[docs.rs Writing Applications](https://docs.rs/ratatui/latest/ratatui/)）
- **事件:** **不含**内置 input；用 backend 库（通常 crossterm）读事件（[docs.rs](https://docs.rs/ratatui/latest/ratatui/)、[Event handling 概念](https://ratatui.rs/concepts/event-handling/)）
- **布局:** constraint-based（类 Flexbox 的 terminal 版）（[官网](https://ratatui.rs/)）
- **Widgets:** Block, Paragraph, Table, Chart, Gauge, List, …（[widgets 文档](https://docs.rs/ratatui/latest/ratatui/widgets/index.html)）
- **Backends:** 默认 **crossterm**；可选 termion / termwiz / termina（[docs.rs Features](https://docs.rs/ratatui/latest/ratatui/)）
- **嵌入式:** 官网强调 pure Rust、`no_std` 相关进展（[ratatui.rs](https://ratatui.rs/)）
- **历史:** 2023 从 [tui-rs](https://crates.io/crates/tui) fork 以继续维护（[GitHub README](https://github.com/ratatui/ratatui)）
- **替代品（官方列出）:** Cursive（ncurses-based）、iocraft（declarative）

#### 2. crossterm（默认底层终端库）

- **Repo:** [github.com/crossterm-rs/crossterm](https://github.com/crossterm-rs/crossterm)
- **Stars:** 约 **4.1k**
- **定位:** pure Rust 跨平台 terminal manipulation（[README](https://github.com/crossterm-rs/crossterm)）
- **能力:** cursor、样式、raw/alt screen、键盘/鼠标/resize 事件、async event-stream feature
- **平台:** UNIX + Windows（文档称 down to Windows 7）
- **Used by:** Ratatui、Cursive、Broot 等

#### 3. termion（Unix 向底层库）

- **Repo:** [github.com/redox-os/termion](https://github.com/redox-os/termion)（镜像；上游 GitLab）
- **Stars:** 约 **2.2k**
- **定位:** pure Rust、bindless、无 ncurses；Raw mode、TrueColor、鼠标等（[README](https://github.com/redox-os/termion)）
- **平台:** Redox / macOS / BSD / Linux（ANSI）；**非 Windows 主力**（Ratatui 文档中 termion feature 标 non-Windows）
- **角色:** Ratatui 可选 backend，而非高层 TUI 框架

#### 4. iced？

- iced 是 **GUI** 框架，非 terminal-first；**本调研不将其列为 TUI 主选项**（无官方「TUI 框架」定位）。

#### 5. 语言/运行时注意

- Rust **标准库无 TUI**；需 Ratatui + backend
- 所有权与类型系统利于大型长期维护；编译时间与学习曲线更高
- 分发：单静态二进制优秀；binary size 通常大于同等 Go（视依赖而定）

### Strengths / trade-offs

| 维度 | 评价 |
|------|------|
| 性能 | 官方强调 sub-ms rendering、zero-cost abstractions（[ratatui.rs](https://ratatui.rs/)） |
| 架构 | Immediate-mode 灵活，但应用骨架需自建（或用 template） |
| 类型安全 | 最强；适合复杂状态机与系统工具 |
| 文档 | ratatui.rs tutorials / recipes / forum 较完整 |
| 跨平台 | crossterm 路径最稳；termion 限 Unix |
| 开发体验 | 编译慢于 Go/TS；错误信息质量高 |
| 分发 | 单二进制；无 Node runtime |

### Sources

- https://ratatui.rs/  
- https://ratatui.rs/concepts/rendering/  
- https://docs.rs/ratatui/latest/ratatui/  
- https://github.com/ratatui/ratatui  
- https://github.com/crossterm-rs/crossterm  
- https://github.com/redox-os/termion  
- https://crates.io/crates/ratatui  

---

## TypeScript

### Frameworks

#### 1. Ink（主流推荐：React for CLIs）

- **Repo:** [github.com/vadimdemedes/ink](https://github.com/vadimdemedes/ink)
- **Stars:** 约 **39.4k**（API，2026-07-19）
- **npm:** 近 30 日下载约 **16.8M**（[npm downloads API](https://api.npmjs.org/downloads/point/last-month/ink)）；持续维护（pushed 2026-07-16）
- **架构:** **React renderer for the terminal** — 组件树 + reconciler；布局用 **Yoga Flexbox**（[README](https://github.com/vadimdemedes/ink)）  
  - 声明式 / retained 组件心智（与 Ratatui immediate-mode 对照鲜明）  
  - 熟悉 React 即可迁移；hooks：`useInput`、`useApp`、`useStdout`、`useFocus` 等
- **能力:** `<Box>` / `<Text>` / `<Static>`、颜色与边框、focus、paste、suspend terminal 交给 `$EDITOR` 等（[README](https://github.com/vadimdemedes/ink)）
- **谁在用（官方列表，节选）:** Claude Code、Gemini CLI、GitHub Copilot CLI、Cloudflare Wrangler、Shopify CLI、Prisma、Gatsby、Terraform CDK 等
- **脚手架:** `npx create-ink-app` / `--typescript`
- **定位边界:** 官方 slogan 是 **React for interactive command-line apps**，强项是 **CLI 交互输出**（进度、表单、向导）；超大型全屏 dashboard 也能做，但社区更常见「增强 CLI」形态

#### 2. OpenTUI（现代 native-core 路线）

- **Site / repo:** [opentui.com](https://opentui.com/)、[github.com/anomalyco/opentui](https://github.com/anomalyco/opentui)
- **Stars:** 约 **12.6k**
- **Version:** v0.4.5（2026-07-17）
- **架构:** **Zig native core** + TypeScript bindings + **C ABI**（可其他语言）；组件式 + **Yoga flexbox**；官方 **React / Solid** reconciler（[@opentui/react](https://github.com/anomalyco/opentui)、[@opentui/solid](https://github.com/anomalyco/opentui)）
- **能力（官网）:** Text/Box/Input/Select/ScrollBox/Code/Diff；tree-sitter 语法高亮；Timeline 动画；性能与正确性导向
- **生产:** powers **OpenCode**；计划 power **terminal.shop**（[opentui.com](https://opentui.com/)、[README](https://github.com/anomalyco/opentui)）
- **npm:** `@opentui/core` 近 30 日约 **1.55M** downloads
- **注意:** README 写构建需系统安装 **Zig**；分发时需考虑 native binary / platform matrix
- **成熟度:** 版本仍为 0.x，迭代快；星标与下载已不可忽视

#### 3. blessed（经典 DOM 式全屏 TUI）

- **Repo:** [github.com/chjj/blessed](https://github.com/chjj/blessed)
- **Stars:** 约 **11.9k**
- **架构:** **DOM-like widget tree** + 自研 terminfo/termcap；damage buffer 优化渲染（[README](https://github.com/chjj/blessed)）
- **能力:** 大量 widget（list, form, table, progress, terminal, image…）；mouse/keyboard
- **维护信号:** 最后 push 约 **2024-03**（调研日 API）；仍大量历史项目依赖  
- **npm:** 近 30 日约 **5.5M** downloads（存量依赖大）

#### 4. terminal-kit（全能终端工具库）

- **Repo:** [github.com/cronvel/terminal-kit](https://github.com/cronvel/terminal-kit)
- **Stars:** 约 **3.4k**
- **定位:** 颜色/样式、输入、菜单、progress bar、screen buffer（含 32-bit composition、图像）、document model 等（[README](https://github.com/cronvel/terminal-kit)）
- **特点:** **不依赖 ncurses**；从简单样式到复杂 app 均可
- **npm:** 近 30 日约 **1.2M** downloads
- **维护:** pushed 2026-06-29，仍活跃

#### 5. 语言/运行时注意

- Node 提供 `tty` / raw mode / streams，但 **无标准 TUI 组件框架**
- 分发：需 Node/Bun runtime，或打包（pkg/bun compile 等）— 一般不如 Go/Rust 单文件自然
- 并发：async event loop；适合 IO 密集 CLI，CPU 密集渲染弱于 Rust
- 类型：TypeScript 可选强类型；运行时仍动态

### Strengths / trade-offs

| 维度 | 评价 |
|------|------|
| 开发者体验 | React 生态最大红利（Ink / OpenTUI React） |
| 全屏 TUI 成熟度 | blessed 经典但老化；OpenTUI 新且有 native 性能叙事 |
| 分发 | 弱于 Go/Rust（runtime / node_modules） |
| 性能 | JS 足够多数 CLI；高刷新 dashboard 更吃力；OpenTUI 用 Zig 补齐 |
| 生态复用 | 与 web 前端人才与库共享 |
| 嵌入/脚本 | npm 发布、脚本集成极易 |

### Sources

- https://github.com/vadimdemedes/ink  
- https://www.npmjs.com/package/ink  
- https://opentui.com/  
- https://github.com/anomalyco/opentui  
- https://github.com/chjj/blessed  
- https://github.com/cronvel/terminal-kit  

---

## Side-by-side comparison

| Dimension | Go | Rust | TypeScript |
|-----------|----|------|------------|
| **主流高层框架** | Bubble Tea（Elm）; tview（widgets） | Ratatui（immediate-mode） | Ink（React）; OpenTUI（Zig+TS） |
| **底层终端** | tcell（或 Bubble Tea 自管） | crossterm / termion / termwiz | Node tty + 各库自实现 / OpenTUI native |
| **架构模型** | TEA（Bubble Tea）或 retained widgets（tview） | Immediate-mode + 自管 event loop | Declarative React / component tree（retained reconciler） |
| **官方文档成熟度** | README + pkg.go.dev + 大量 examples | ratatui.rs book-style site + docs.rs | Ink 长 README；OpenTUI 独立 docs 站 |
| **生态热度信号（约）** | bubbletea ~44k★；tview ~14k★ | ratatui ~22k★ / 38M+ crates downloads | ink ~39k★ / ~17M npm/mo；opentui ~13k★ |
| **二进制分发** | 优秀 | 优秀 | 通常需 runtime 或额外打包 |
| **性能** | 高 | 通常最高 | 中（OpenTUI native 可更高） |
| **并发模型** | goroutines + Cmd messages | threads / async（tokio 可选） | event loop + promises |
| **跨平台终端** | 强（tcell/Bubble Tea） | 强（crossterm） | 强（视库；blessed 历史 Windows 坑较多） |
| **类型安全** | 静态 | 最强 | TS 静态 + 运行时动态 |
| **学习曲线** | 低–中 | 中–高 | 低（若已会 React） |
| **适合 UI 形态** | 全屏 TUI、inline、forms | 全屏 dashboard、工具、游戏 | CLI 交互、向导、部分全屏 |
| **嵌入/脚本** | 可库引用；gum 等 shell 友好 | 库引用 | npm 最强；易进 monorepo |

### 架构对照（便于选型）

```
Bubble Tea:   Msg → Update(Model) → View(string)     // Elm / TEA
tview:        Widget tree 事件回调 + Application.Run  // Retained
Ratatui:      loop { draw(state); handle_event() }   // Immediate-mode
Ink:          React state → reconciler → terminal     // Declarative React
OpenTUI:      Components / React|Solid → Zig core     // Hybrid native
```

---

## When to choose which

### 选 **Go** 当：

1. 产品是要 **安装即用的 CLI/TUI 工具**，希望 `curl | tar` 一个二进制。  
2. 团队要 **可维护的全屏应用** 且偏好 **Elm 单向数据流** → **Bubble Tea + Bubbles + Lip Gloss**。  
3. 主要是 **多步表单/确认框** → **Huh**（可再嵌进 Bubble Tea）。  
4. 需要 **表格/树/多页 layout 开箱即用** 且喜欢命令式 API → **tview + tcell**。  
5. 组织已在用 Go 后端/运维栈，希望统一语言。

**不优先 Go 当：** 团队全是 React 且只做轻量 CLI 进度 UI（Ink 更快上手）；或需要极致帧率与零成本抽象（Rust）。

### 选 **Rust** 当：

1. 工具偏 **系统/可观测/安全/嵌入**，要内存安全与高性能渲染。  
2. 接受 **自己拼装 app 架构**（或使用官方 templates），拥抱 **immediate-mode**。  
3. 已有 Rust 代码库，TUI 只是外壳（如 OpenAI Codex、各类 dashboard 案例）。  
4. 需要 **crossterm 级**细粒度终端控制与 async 事件流。

**不优先 Rust 当：** 原型速度第一、团队无 Rust 经验、或交付渠道强依赖 npm。

### 选 **TypeScript** 当：

1. 团队是 **前端/全栈 React**，要快速做交互 CLI → **Ink**。  
2. 已在 Node 生态发版（`npx`、VS Code 扩展伴随工具等）。  
3. 需要 **现代全屏 + 原生性能叙事** 且可接受 Zig/native 构建矩阵 → **OpenTUI**。  
4. 维护旧项目已用 **blessed / terminal-kit** → 延续，但新项目谨慎评估维护节奏。

**不优先 TypeScript 当：** 必须以最小依赖静态二进制交付到受限环境；或 CPU 密集实时刷新是硬指标且不愿引入 native 核心。

### 实用决策树

```
需要单文件原生二进制？
├─ 是 → 团队语言？
│        ├─ Go 熟悉 → Bubble Tea（或 tview）
│        └─ 要极限性能/已有 Rust → Ratatui + crossterm
└─ 否（Node 可接受）
         ├─ 主要是 CLI 交互/向导 → Ink
         ├─ 全屏复杂 TUI + 愿用 native → OpenTUI
         └─ 遗留代码 → blessed / terminal-kit
```

---

## Sources appendix

### Go

| Source | URL |
|--------|-----|
| Bubble Tea README | https://github.com/charmbracelet/bubbletea |
| Bubble Tea pkg.go.dev | https://pkg.go.dev/github.com/charmbracelet/bubbletea |
| Charm home | https://charm.land/ |
| Bubbles | https://github.com/charmbracelet/bubbles |
| Lip Gloss | https://github.com/charmbracelet/lipgloss |
| Huh | https://github.com/charmbracelet/huh |
| tcell | https://github.com/gdamore/tcell |
| tview | https://github.com/rivo/tview |
| Elm Architecture（Bubble Tea 所基于） | https://guide.elm-lang.org/architecture/ |

### Rust

| Source | URL |
|--------|-----|
| Ratatui website | https://ratatui.rs/ |
| Ratatui rendering concept | https://ratatui.rs/concepts/rendering/ |
| Ratatui tutorials | https://ratatui.rs/tutorials/ |
| docs.rs ratatui | https://docs.rs/ratatui/latest/ratatui/ |
| GitHub ratatui | https://github.com/ratatui/ratatui |
| crossterm | https://github.com/crossterm-rs/crossterm |
| termion | https://github.com/redox-os/termion |
| crates.io ratatui | https://crates.io/crates/ratatui |

### TypeScript

| Source | URL |
|--------|-----|
| Ink | https://github.com/vadimdemedes/ink |
| npm ink | https://www.npmjs.com/package/ink |
| OpenTUI site | https://opentui.com/ |
| OpenTUI GitHub | https://github.com/anomalyco/opentui |
| blessed | https://github.com/chjj/blessed |
| terminal-kit | https://github.com/cronvel/terminal-kit |
| Yoga（Ink/OpenTUI 布局） | https://github.com/facebook/yoga |

### 时间敏感指标（2026-07-19 快照）

| 项目 | 指标 | 来源 |
|------|------|------|
| bubbletea | ~43.8k stars; v2.0.8 (2026-07-03) | GitHub |
| tview | ~14k stars | GitHub |
| tcell | ~5.2k stars; v3.4.0 (2026-05-17) | GitHub |
| ratatui | ~21.8k stars; 0.30.2; 官网 38.2M downloads / 4800+ reverse deps | GitHub / ratatui.rs |
| crossterm | ~4.1k stars | GitHub |
| termion | ~2.2k stars | GitHub |
| ink | ~39.4k stars; ~16.8M npm downloads/month | GitHub API / npm API |
| OpenTUI | ~12.6k stars; v0.4.5; @opentui/core ~1.55M npm/month | GitHub / npm API |
| blessed | ~11.9k stars; last push ~2024-03; ~5.5M npm/month | GitHub API / npm API |
| terminal-kit | ~3.4k stars; ~1.2M npm/month | GitHub / npm API |
| huh | ~7k stars | GitHub |
| lipgloss | ~11.6k stars | GitHub |
| bubbles | ~8.7k stars | GitHub |

*星标与下载量会变动；引用时请复核第一方页面。*
