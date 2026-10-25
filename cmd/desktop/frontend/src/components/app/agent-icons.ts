// Agent 的品牌图标：照搬 @lobehub/icons 的 agentConfig(顺序与关键词一致),按名字认出是哪个 Agent。
// 只按路径导入每个图标里纯 SVG 的组件与主色：有彩色版用彩色版，没有的用单色版;包入口和 Avatar 会带上 @lobehub/ui,不用。
import type { IconType } from "@lobehub/icons/es/types";
import LobeHub from "@lobehub/icons/es/LobeHub/components/Color";
import * as LobeHubStyle from "@lobehub/icons/es/LobeHub/style";
import ClaudeCode from "@lobehub/icons/es/ClaudeCode/components/Color";
import * as ClaudeCodeStyle from "@lobehub/icons/es/ClaudeCode/style";
import Claude from "@lobehub/icons/es/Claude/components/Color";
import * as ClaudeStyle from "@lobehub/icons/es/Claude/style";
import Codex from "@lobehub/icons/es/Codex/components/Color";
import * as CodexStyle from "@lobehub/icons/es/Codex/style";
import OpenAI from "@lobehub/icons/es/OpenAI/components/Mono";
import * as OpenAIStyle from "@lobehub/icons/es/OpenAI/style";
import Kimi from "@lobehub/icons/es/Kimi/components/Color";
import * as KimiStyle from "@lobehub/icons/es/Kimi/style";
import OpenClaw from "@lobehub/icons/es/OpenClaw/components/Color";
import * as OpenClawStyle from "@lobehub/icons/es/OpenClaw/style";
import HermesAgent from "@lobehub/icons/es/HermesAgent/components/Mono";
import * as HermesAgentStyle from "@lobehub/icons/es/HermesAgent/style";
import Manus from "@lobehub/icons/es/Manus/components/Mono";
import * as ManusStyle from "@lobehub/icons/es/Manus/style";
import CherryStudio from "@lobehub/icons/es/CherryStudio/components/Color";
import * as CherryStudioStyle from "@lobehub/icons/es/CherryStudio/style";
import Cursor from "@lobehub/icons/es/Cursor/components/Mono";
import * as CursorStyle from "@lobehub/icons/es/Cursor/style";
import Amp from "@lobehub/icons/es/Amp/components/Color";
import * as AmpStyle from "@lobehub/icons/es/Amp/style";
import GithubCopilot from "@lobehub/icons/es/GithubCopilot/components/Mono";
import * as GithubCopilotStyle from "@lobehub/icons/es/GithubCopilot/style";
import Copilot from "@lobehub/icons/es/Copilot/components/Color";
import * as CopilotStyle from "@lobehub/icons/es/Copilot/style";
import GeminiCLI from "@lobehub/icons/es/GeminiCLI/components/Color";
import * as GeminiCLIStyle from "@lobehub/icons/es/GeminiCLI/style";
import Gemini from "@lobehub/icons/es/Gemini/components/Color";
import * as GeminiStyle from "@lobehub/icons/es/Gemini/style";
import KiloCode from "@lobehub/icons/es/KiloCode/components/Mono";
import * as KiloCodeStyle from "@lobehub/icons/es/KiloCode/style";
import Junie from "@lobehub/icons/es/Junie/components/Color";
import * as JunieStyle from "@lobehub/icons/es/Junie/style";
import Replit from "@lobehub/icons/es/Replit/components/Color";
import * as ReplitStyle from "@lobehub/icons/es/Replit/style";
import Windsurf from "@lobehub/icons/es/Windsurf/components/Mono";
import * as WindsurfStyle from "@lobehub/icons/es/Windsurf/style";
import Cline from "@lobehub/icons/es/Cline/components/Mono";
import * as ClineStyle from "@lobehub/icons/es/Cline/style";
import OpenCode from "@lobehub/icons/es/OpenCode/components/Mono";
import * as OpenCodeStyle from "@lobehub/icons/es/OpenCode/style";
import OpenHands from "@lobehub/icons/es/OpenHands/components/Color";
import * as OpenHandsStyle from "@lobehub/icons/es/OpenHands/style";
import RooCode from "@lobehub/icons/es/RooCode/components/Mono";
import * as RooCodeStyle from "@lobehub/icons/es/RooCode/style";
import Goose from "@lobehub/icons/es/Goose/components/Mono";
import * as GooseStyle from "@lobehub/icons/es/Goose/style";
import Trae from "@lobehub/icons/es/Trae/components/Color";
import * as TraeStyle from "@lobehub/icons/es/Trae/style";
import Zencoder from "@lobehub/icons/es/Zencoder/components/Color";
import * as ZencoderStyle from "@lobehub/icons/es/Zencoder/style";
import Coze from "@lobehub/icons/es/Coze/components/Mono";
import * as CozeStyle from "@lobehub/icons/es/Coze/style";
import Dify from "@lobehub/icons/es/Dify/components/Color";
import * as DifyStyle from "@lobehub/icons/es/Dify/style";
import Antigravity from "@lobehub/icons/es/Antigravity/components/Color";
import * as AntigravityStyle from "@lobehub/icons/es/Antigravity/style";
import Qoder from "@lobehub/icons/es/Qoder/components/Color";
import * as QoderStyle from "@lobehub/icons/es/Qoder/style";
import Qwen from "@lobehub/icons/es/Qwen/components/Color";
import * as QwenStyle from "@lobehub/icons/es/Qwen/style";
import Microsoft from "@lobehub/icons/es/Microsoft/components/Color";
import * as MicrosoftStyle from "@lobehub/icons/es/Microsoft/style";
import OpenWebUI from "@lobehub/icons/es/OpenWebUI/components/Mono";
import * as OpenWebUIStyle from "@lobehub/icons/es/OpenWebUI/style";
import CodeFlicker from "@lobehub/icons/es/CodeFlicker/components/Color";
import * as CodeFlickerStyle from "@lobehub/icons/es/CodeFlicker/style";
import Flowith from "@lobehub/icons/es/Flowith/components/Mono";
import * as FlowithStyle from "@lobehub/icons/es/Flowith/style";
import ZeroOne from "@lobehub/icons/es/ZeroOne/components/Color";
import * as ZeroOneStyle from "@lobehub/icons/es/ZeroOne/style";
import MetaAI from "@lobehub/icons/es/MetaAI/components/Color";
import * as MetaAIStyle from "@lobehub/icons/es/MetaAI/style";
import N8n from "@lobehub/icons/es/N8n/components/Color";
import * as N8nStyle from "@lobehub/icons/es/N8n/style";
import Notion from "@lobehub/icons/es/Notion/components/Mono";
import * as NotionStyle from "@lobehub/icons/es/Notion/style";
import CodeBuddy from "@lobehub/icons/es/CodeBuddy/components/Color";
import * as CodeBuddyStyle from "@lobehub/icons/es/CodeBuddy/style";
import Devin from "@lobehub/icons/es/Devin/components/Color";
import * as DevinStyle from "@lobehub/icons/es/Devin/style";

type Style = { COLOR_PRIMARY: string };

const brands: [IconType, Style, string[]][] = [
  [LobeHub, LobeHubStyle, ["lobehub", "lobechat", "lobe-chat", "lobe-agent", "lobe-ai", "lobeai", "lobe"]],
  [ClaudeCode, ClaudeCodeStyle, ["claude-code", "claudecode", "claude-co"]],
  [Claude, ClaudeStyle, ["claude-desktop", "open-claude", "claude"]],
  [Codex, CodexStyle, ["codex"]],
  [OpenAI, OpenAIStyle, ["openai", "chatgpt", "gpt"]],
  [Kimi, KimiStyle, ["kimi-cli", "kimi-code", "kimi", "kimiclaw", "kimi-claw"]],
  [OpenClaw, OpenClawStyle, ["open-claw", "openclaw", "dewu-smartclaw", "nanoclaw", "zeroclaw", "goclaw", "clawhub", "catpaw", "copaw", "clawdbot", "moltbot", "claw"]],
  [HermesAgent, HermesAgentStyle, ["hermes"]],
  [Manus, ManusStyle, ["manus"]],
  [CherryStudio, CherryStudioStyle, ["cherry-studio", "cherrystudio"]],
  [Cursor, CursorStyle, ["cursor"]],
  [Amp, AmpStyle, ["amp"]],
  [GithubCopilot, GithubCopilotStyle, ["github-copilot-cli", "github-copilot", "githubcopilot", "vscode-copilot", "copilot-cli", "github"]],
  [Copilot, CopilotStyle, ["copilot"]],
  [GeminiCLI, GeminiCLIStyle, ["geminicli", "gemini-cli"]],
  [Gemini, GeminiStyle, ["gemini-scout", "gemini"]],
  [KiloCode, KiloCodeStyle, ["kilo-code", "kilocode", "kilo", "kiro-cli", "kiro"]],
  [Junie, JunieStyle, ["junie"]],
  [Replit, ReplitStyle, ["replit"]],
  [Windsurf, WindsurfStyle, ["windsurf"]],
  [Cline, ClineStyle, ["cline"]],
  [OpenCode, OpenCodeStyle, ["open-code", "opencode", "openwork", "open-code-go", "opencodego", "opencodecodingplan", "open-code-zen", "opencodezen"]],
  [OpenHands, OpenHandsStyle, ["open-hands", "openhands"]],
  [RooCode, RooCodeStyle, ["roo-code", "roocode"]],
  [Goose, GooseStyle, ["goose"]],
  [Trae, TraeStyle, ["trae-ide", "trae-ai", "traeb", "trae"]],
  [Zencoder, ZencoderStyle, ["zencoder"]],
  [Coze, CozeStyle, ["coze-coding", "coze"]],
  [Dify, DifyStyle, ["dify"]],
  [Antigravity, AntigravityStyle, ["antigravity", "google"]],
  [Qoder, QoderStyle, ["qoder-work", "qoder"]],
  [Qwen, QwenStyle, ["qwen-code", "qwen"]],
  [Microsoft, MicrosoftStyle, ["windows", "vscode", "micode"]],
  [OpenWebUI, OpenWebUIStyle, ["openwebui-bridge", "openwebui"]],
  [CodeFlicker, CodeFlickerStyle, ["codeflicker"]],
  [Flowith, FlowithStyle, ["iflow", "flowith"]],
  [ZeroOne, ZeroOneStyle, ["agent-zero", "agentzero", "agent0"]],
  [MetaAI, MetaAIStyle, ["meta-ai", "metaai"]],
  [N8n, N8nStyle, ["n8n"]],
  [Notion, NotionStyle, ["notion"]],
  [CodeBuddy, CodeBuddyStyle, ["code-buddy", "codebuddy"]],
  [Devin, DevinStyle, ["devin", "cognition"]],
];

export type AgentBrand = { Icon: IconType; color: string };

// agentBrand 按名字找 Agent 的品牌图标，认不出时返回 undefined。名字里的空格当连字符,
// 包含某个关键词就算，靠前的先命中("Claude Code" 认成 Claude Code,不是 Claude)。
// color 是单色版图标在白底上的颜色：品牌主色，太浅(白、浅黄)看不清时用黑色。
export function agentBrand(name: string): AgentBrand | undefined {
  const key = name.trim().toLowerCase().replace(/[\s_]+/g, "-");
  if (!key) return undefined;
  const found = brands.find(([, , keywords]) => keywords.some((k) => key.includes(k)));
  if (!found) return undefined;
  const [Icon, style] = found;
  return { Icon, color: light(style.COLOR_PRIMARY) ? "#000" : style.COLOR_PRIMARY };
}

function light(hex: string): boolean {
  const h = hex.slice(1);
  const full = h.length === 3 ? [...h].map((c) => c + c).join("") : h;
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16) / 255);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.75;
}
