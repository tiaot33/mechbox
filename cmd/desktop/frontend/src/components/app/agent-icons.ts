// Agent 的品牌图标，按名字认出是哪个 Agent。图标有两个来源:
// - agentlogos.dev 的彩色 SVG,放在 agent-logos/ 里(从 https://agentlogos.dev/logos.json 列出的 url 下载)。两边都有的用这里的。
// - @lobehub/icons:agentlogos.dev 没有的，以及 lobe 那张更好认的(Claude Code、Gemini CLI、Kiro、Amp)。
// 关键词表照搬 lobe 的 agentConfig(顺序一致),不同之处:
// - Kiro 单列一项(lobe 把它算进 Kilo Code);OpenAI 与 ChatGPT 分成两项，各用 agentlogos.dev 的图标。
// - Windsurf 多一个 agentlogos.dev 给的别名 Devin Desktop。
// - 另加 lobe 没有、agentlogos.dev 有的编程 Agent,接在表尾;Jules、Firebase Studio 必须排在 Antigravity 前面，
//   否则名字里带 Google 的会先被 Antigravity 的 "google" 认走。
// lobe 只按路径导入每个图标里纯 SVG 的组件与主色：有彩色版用彩色版，没有的用单色版;包入口和 Avatar 会带上 @lobehub/ui,不用。
import type { IconType } from "@lobehub/icons/es/types";
import LobeHub from "@lobehub/icons/es/LobeHub/components/Color";
import * as LobeHubStyle from "@lobehub/icons/es/LobeHub/style";
import ClaudeCode from "@lobehub/icons/es/ClaudeCode/components/Color";
import * as ClaudeCodeStyle from "@lobehub/icons/es/ClaudeCode/style";
import OpenClaw from "@lobehub/icons/es/OpenClaw/components/Color";
import * as OpenClawStyle from "@lobehub/icons/es/OpenClaw/style";
import HermesAgent from "@lobehub/icons/es/HermesAgent/components/Mono";
import * as HermesAgentStyle from "@lobehub/icons/es/HermesAgent/style";
import CherryStudio from "@lobehub/icons/es/CherryStudio/components/Color";
import * as CherryStudioStyle from "@lobehub/icons/es/CherryStudio/style";
import Amp from "@lobehub/icons/es/Amp/components/Color";
import * as AmpStyle from "@lobehub/icons/es/Amp/style";
import GeminiCLI from "@lobehub/icons/es/GeminiCLI/components/Color";
import * as GeminiCLIStyle from "@lobehub/icons/es/GeminiCLI/style";
import Kiro from "@lobehub/icons/es/Kiro/components/Color";
import * as KiroStyle from "@lobehub/icons/es/Kiro/style";
import Zencoder from "@lobehub/icons/es/Zencoder/components/Color";
import * as ZencoderStyle from "@lobehub/icons/es/Zencoder/style";
import Coze from "@lobehub/icons/es/Coze/components/Mono";
import * as CozeStyle from "@lobehub/icons/es/Coze/style";
import Dify from "@lobehub/icons/es/Dify/components/Color";
import * as DifyStyle from "@lobehub/icons/es/Dify/style";
import Qoder from "@lobehub/icons/es/Qoder/components/Color";
import * as QoderStyle from "@lobehub/icons/es/Qoder/style";
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
import N8n from "@lobehub/icons/es/N8n/components/Color";
import * as N8nStyle from "@lobehub/icons/es/N8n/style";
import Notion from "@lobehub/icons/es/Notion/components/Mono";
import * as NotionStyle from "@lobehub/icons/es/Notion/style";
import CodeBuddy from "@lobehub/icons/es/CodeBuddy/components/Color";
import * as CodeBuddyStyle from "@lobehub/icons/es/CodeBuddy/style";
import aider from "./agent-logos/aider.svg";
import amazonQ from "./agent-logos/amazon-q.svg";
import antigravity from "./agent-logos/antigravity.svg";
import augmentCode from "./agent-logos/augment-code.svg";
import chatgpt from "./agent-logos/chatgpt.svg";
import claude from "./agent-logos/claude.svg";
import cline from "./agent-logos/cline.svg";
import codeium from "./agent-logos/codeium.svg";
import codex from "./agent-logos/codex.svg";
import cody from "./agent-logos/cody.svg";
import continueDev from "./agent-logos/continue.svg";
import cursor from "./agent-logos/cursor.svg";
import devin from "./agent-logos/devin.svg";
import factory from "./agent-logos/factory.svg";
import firebaseStudio from "./agent-logos/firebase-studio.svg";
import gemini from "./agent-logos/gemini.svg";
import githubCopilot from "./agent-logos/github-copilot.svg";
import goose from "./agent-logos/goose.svg";
import ibmBob from "./agent-logos/ibm-bob.svg";
import jules from "./agent-logos/jules.svg";
import junie from "./agent-logos/junie.svg";
import kiloCode from "./agent-logos/kilo-code.svg";
import kimi from "./agent-logos/kimi.svg";
import manus from "./agent-logos/manus.svg";
import metaAI from "./agent-logos/meta-ai.svg";
import microsoftCopilot from "./agent-logos/microsoft-copilot.svg";
import openai from "./agent-logos/openai.svg";
import opencode from "./agent-logos/opencode.svg";
import openhands from "./agent-logos/openhands.svg";
import pi from "./agent-logos/pi.svg";
import qodo from "./agent-logos/qodo.svg";
import qwen from "./agent-logos/qwen.svg";
import replit from "./agent-logos/replit.svg";
import rooCode from "./agent-logos/roo-code.svg";
import tabnine from "./agent-logos/tabnine.svg";
import trae from "./agent-logos/trae.svg";
import warp from "./agent-logos/warp.svg";
import windsurf from "./agent-logos/windsurf.svg";
import zed from "./agent-logos/zed.svg";

// AgentBrand 是认出的品牌图标:src 是 agentlogos.dev 的彩色 SVG 地址;Icon 是 lobe 的图标组件,
// color 是它在白底上的颜色(单色版用品牌主色，太浅看不清时用黑色)。
export type AgentBrand = { src: string } | { Icon: IconType; color: string };

const brands: [AgentBrand, string[]][] = [
  [lobe(LobeHub, LobeHubStyle), ["lobehub", "lobechat", "lobe-chat", "lobe-agent", "lobe-ai", "lobeai", "lobe"]],
  [lobe(ClaudeCode, ClaudeCodeStyle), ["claude-code", "claudecode", "claude-co"]],
  [{ src: claude }, ["claude-desktop", "open-claude", "claude"]],
  [{ src: codex }, ["codex"]],
  [{ src: openai }, ["openai"]],
  [{ src: chatgpt }, ["chatgpt", "gpt"]],
  [{ src: kimi }, ["kimi-cli", "kimi-code", "kimi", "kimiclaw", "kimi-claw"]],
  [lobe(OpenClaw, OpenClawStyle), ["open-claw", "openclaw", "dewu-smartclaw", "nanoclaw", "zeroclaw", "goclaw", "clawhub", "catpaw", "copaw", "clawdbot", "moltbot", "claw"]],
  [lobe(HermesAgent, HermesAgentStyle), ["hermes"]],
  [{ src: manus }, ["manus"]],
  [lobe(CherryStudio, CherryStudioStyle), ["cherry-studio", "cherrystudio"]],
  [{ src: cursor }, ["cursor"]],
  [lobe(Amp, AmpStyle), ["amp"]],
  [{ src: githubCopilot }, ["github-copilot-cli", "github-copilot", "githubcopilot", "vscode-copilot", "copilot-cli", "github"]],
  [{ src: microsoftCopilot }, ["copilot"]],
  [lobe(GeminiCLI, GeminiCLIStyle), ["geminicli", "gemini-cli"]],
  [{ src: gemini }, ["gemini-scout", "gemini"]],
  [lobe(Kiro, KiroStyle), ["kiro-cli", "kiro"]],
  [{ src: kiloCode }, ["kilo-code", "kilocode", "kilo"]],
  [{ src: junie }, ["junie"]],
  [{ src: replit }, ["replit"]],
  [{ src: windsurf }, ["windsurf", "devin-desktop"]],
  [{ src: cline }, ["cline"]],
  [{ src: opencode }, ["open-code", "opencode", "openwork", "open-code-go", "opencodego", "opencodecodingplan", "open-code-zen", "opencodezen"]],
  [{ src: openhands }, ["open-hands", "openhands"]],
  [{ src: rooCode }, ["roo-code", "roocode"]],
  [{ src: goose }, ["goose"]],
  [{ src: trae }, ["trae-ide", "trae-ai", "traeb", "trae"]],
  [lobe(Zencoder, ZencoderStyle), ["zencoder"]],
  [lobe(Coze, CozeStyle), ["coze-coding", "coze"]],
  [lobe(Dify, DifyStyle), ["dify"]],
  [{ src: jules }, ["jules"]],
  [{ src: firebaseStudio }, ["firebase"]],
  [{ src: antigravity }, ["antigravity", "google"]],
  [lobe(Qoder, QoderStyle), ["qoder-work", "qoder"]],
  [{ src: qwen }, ["qwen-code", "qwen"]],
  [lobe(Microsoft, MicrosoftStyle), ["windows", "vscode", "micode"]],
  [lobe(OpenWebUI, OpenWebUIStyle), ["openwebui-bridge", "openwebui"]],
  [lobe(CodeFlicker, CodeFlickerStyle), ["codeflicker"]],
  [lobe(Flowith, FlowithStyle), ["iflow", "flowith"]],
  [lobe(ZeroOne, ZeroOneStyle), ["agent-zero", "agentzero", "agent0"]],
  [{ src: metaAI }, ["meta-ai", "metaai"]],
  [lobe(N8n, N8nStyle), ["n8n"]],
  [lobe(Notion, NotionStyle), ["notion"]],
  [lobe(CodeBuddy, CodeBuddyStyle), ["code-buddy", "codebuddy"]],
  [{ src: devin }, ["devin", "cognition"]],
  [{ src: aider }, ["-aider-"]],
  [{ src: augmentCode }, ["-augment-", "augmentcode", "auggie"]],
  [{ src: cody }, ["sourcegraph", "cody"]],
  [{ src: qodo }, ["qodo"]],
  [{ src: zed }, ["-zed-"]],
  [{ src: warp }, ["-warp-"]],
  [{ src: continueDev }, ["-continue-"]],
  [{ src: amazonQ }, ["amazon-q", "amazonq", "q-developer"]],
  [{ src: factory }, ["factory-droid", "factory-ai", "-droid-"]],
  [{ src: pi }, ["-pi-"]],
  [{ src: tabnine }, ["tabnine"]],
  [{ src: codeium }, ["codeium"]],
  [{ src: ibmBob }, ["ibm-bob", "-bob-"]],
];

// agentBrand 按名字找 Agent 的品牌图标，认不出时返回 undefined。名字里的空格当连字符,
// 包含某个关键词就算，靠前的先命中("Claude Code" 认成 Claude Code,不是 Claude)。
// 两头带连字符的关键词(如 "-pi-")只认整个词，用于很短或是常见英文单词的关键词,
// 免得 "API Helper"、"Android Studio"、"Augmented Coder" 这类名字被认成 Pi、Droid、Augment Code。
export function agentBrand(name: string): AgentBrand | undefined {
  const key = name.trim().toLowerCase().replace(/[\s_]+/g, "-");
  if (!key) return undefined;
  const padded = `-${key}-`;
  return brands.find(([, keywords]) => keywords.some((k) => padded.includes(k)))?.[0];
}

function lobe(Icon: IconType, style: { COLOR_PRIMARY: string }): AgentBrand {
  return { Icon, color: light(style.COLOR_PRIMARY) ? "#000" : style.COLOR_PRIMARY };
}

function light(hex: string): boolean {
  const h = hex.slice(1);
  const full = h.length === 3 ? [...h].map((c) => c + c).join("") : h;
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16) / 255);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.75;
}
