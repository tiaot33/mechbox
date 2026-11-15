# Agents

Agent 的品牌图标,59 个彩色 SVG,从 `cmd/desktop/frontend/src/components/app/agent-logos/` 原样搬来(来源是 agentlogos.dev),文件名和代码里的一致。

- 它们是各家的标志,保留原来的颜色,不改色、不描边、不重画。这是界面里两处颜色例外之一。
- 只通过 AgentMark 使用:放在 `mark` 底的小方块里,占方块的 78%。
- 按 Agent 的名字认图标,关键词表在 `agent-icons.ts`。代码里另有 20 个图标取自 @lobehub/icons(Claude Code、Gemini CLI、Kiro、Amp 等),不是文件,没有收进来。
- 认不出的 Agent 不用这里的图标,画灰色的 `assets/Icons/bot.svg`。
