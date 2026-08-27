/**
 * Brand marks for the preset catalogue.
 *
 * The bare `Mono` mark plus the brand's own avatar colours, drawn onto a tile
 * by `ProviderIcon`. Not `@lobehub/icons`' own `Avatar`: that one is built on
 * `@lobehub/ui`, which pulls antd and emoji-mart into the bundle and into
 * every test that touches this screen, to draw a rounded square we already
 * know how to draw.
 *
 * A null background means the brand's own tile is white -- 17 of the
 * 75 are, and six of those are white on white. Those take the neutral
 * tile, and their ink goes with it: a mark drawn for a white tile is as
 * likely to be black, which is invisible on the dark canvas. 5 brands
 * carry a gradient rather than a flat colour, which is why background is a
 * CSS value and not a hex string.
 *
 * Generated against `internal/catalog/presets.yaml`: 87 of the 197
 * presets resolve to a brand, either by name, by the vendor a gateway fronts
 * (`g4f-groq` is groq), or by the family prefix (`cloudflare-ai`). The
 * remaining 110 are small aggregators with no mark anywhere, and
 * they take the monogram in `provider-icon.tsx` -- which is why that fallback
 * is a designed state and not an error case.
 */
import type { ComponentType } from "react"

import Ai21Mono from "@lobehub/icons/es/Ai21/components/Mono"
import AlibabaMono from "@lobehub/icons/es/Alibaba/components/Mono"
import AnthropicMono from "@lobehub/icons/es/Anthropic/components/Mono"
import BaichuanMono from "@lobehub/icons/es/Baichuan/components/Mono"
import BaiduMono from "@lobehub/icons/es/Baidu/components/Mono"
import BailianMono from "@lobehub/icons/es/Bailian/components/Mono"
import BasetenMono from "@lobehub/icons/es/Baseten/components/Mono"
import BedrockMono from "@lobehub/icons/es/Bedrock/components/Mono"
import ByteDanceMono from "@lobehub/icons/es/ByteDance/components/Mono"
import CerebrasMono from "@lobehub/icons/es/Cerebras/components/Mono"
import ChatGLMMono from "@lobehub/icons/es/ChatGLM/components/Mono"
import CloudflareMono from "@lobehub/icons/es/Cloudflare/components/Mono"
import CohereMono from "@lobehub/icons/es/Cohere/components/Mono"
import CozeMono from "@lobehub/icons/es/Coze/components/Mono"
import DbrxMono from "@lobehub/icons/es/Dbrx/components/Mono"
import DeepInfraMono from "@lobehub/icons/es/DeepInfra/components/Mono"
import DeepSeekMono from "@lobehub/icons/es/DeepSeek/components/Mono"
import DifyMono from "@lobehub/icons/es/Dify/components/Mono"
import DoubaoMono from "@lobehub/icons/es/Doubao/components/Mono"
import FeatherlessMono from "@lobehub/icons/es/Featherless/components/Mono"
import FireworksMono from "@lobehub/icons/es/Fireworks/components/Mono"
import FriendliMono from "@lobehub/icons/es/Friendli/components/Mono"
import GeminiMono from "@lobehub/icons/es/Gemini/components/Mono"
import GroqMono from "@lobehub/icons/es/Groq/components/Mono"
import HaiperMono from "@lobehub/icons/es/Haiper/components/Mono"
import HuggingFaceMono from "@lobehub/icons/es/HuggingFace/components/Mono"
import HyperbolicMono from "@lobehub/icons/es/Hyperbolic/components/Mono"
import IdeogramMono from "@lobehub/icons/es/Ideogram/components/Mono"
import InceptionMono from "@lobehub/icons/es/Inception/components/Mono"
import InferenceMono from "@lobehub/icons/es/Inference/components/Mono"
import InternLMMono from "@lobehub/icons/es/InternLM/components/Mono"
import KimiMono from "@lobehub/icons/es/Kimi/components/Mono"
import LambdaMono from "@lobehub/icons/es/Lambda/components/Mono"
import LiquidMono from "@lobehub/icons/es/Liquid/components/Mono"
import LmStudioMono from "@lobehub/icons/es/LmStudio/components/Mono"
import LongCatMono from "@lobehub/icons/es/LongCat/components/Mono"
import MetaMono from "@lobehub/icons/es/Meta/components/Mono"
import MinimaxMono from "@lobehub/icons/es/Minimax/components/Mono"
import MistralMono from "@lobehub/icons/es/Mistral/components/Mono"
import ModelScopeMono from "@lobehub/icons/es/ModelScope/components/Mono"
import MorphMono from "@lobehub/icons/es/Morph/components/Mono"
import NebiusMono from "@lobehub/icons/es/Nebius/components/Mono"
import NousResearchMono from "@lobehub/icons/es/NousResearch/components/Mono"
import NovitaMono from "@lobehub/icons/es/Novita/components/Mono"
import NvidiaMono from "@lobehub/icons/es/Nvidia/components/Mono"
import OllamaMono from "@lobehub/icons/es/Ollama/components/Mono"
import OpenAIMono from "@lobehub/icons/es/OpenAI/components/Mono"
import OpenCodeMono from "@lobehub/icons/es/OpenCode/components/Mono"
import OpenRouterMono from "@lobehub/icons/es/OpenRouter/components/Mono"
import PerplexityMono from "@lobehub/icons/es/Perplexity/components/Mono"
import PollinationsMono from "@lobehub/icons/es/Pollinations/components/Mono"
import PoolsideMono from "@lobehub/icons/es/Poolside/components/Mono"
import QiniuMono from "@lobehub/icons/es/Qiniu/components/Mono"
import QoderMono from "@lobehub/icons/es/Qoder/components/Mono"
import QwenMono from "@lobehub/icons/es/Qwen/components/Mono"
import SambaNovaMono from "@lobehub/icons/es/SambaNova/components/Mono"
import SenseNovaMono from "@lobehub/icons/es/SenseNova/components/Mono"
import SnowflakeMono from "@lobehub/icons/es/Snowflake/components/Mono"
import SparkMono from "@lobehub/icons/es/Spark/components/Mono"
import StepfunMono from "@lobehub/icons/es/Stepfun/components/Mono"
import TencentMono from "@lobehub/icons/es/Tencent/components/Mono"
import TogetherMono from "@lobehub/icons/es/Together/components/Mono"
import UdioMono from "@lobehub/icons/es/Udio/components/Mono"
import UpstageMono from "@lobehub/icons/es/Upstage/components/Mono"
import V0Mono from "@lobehub/icons/es/V0/components/Mono"
import VeniceMono from "@lobehub/icons/es/Venice/components/Mono"
import VercelMono from "@lobehub/icons/es/Vercel/components/Mono"
import VertexAIMono from "@lobehub/icons/es/VertexAI/components/Mono"
import VolcengineMono from "@lobehub/icons/es/Volcengine/components/Mono"
import VoyageMono from "@lobehub/icons/es/Voyage/components/Mono"
import XAIMono from "@lobehub/icons/es/XAI/components/Mono"
import XiaomiMiMoMono from "@lobehub/icons/es/XiaomiMiMo/components/Mono"
import YiMono from "@lobehub/icons/es/Yi/components/Mono"
import ZAIMono from "@lobehub/icons/es/ZAI/components/Mono"
import ZenMuxMono from "@lobehub/icons/es/ZenMux/components/Mono"

export type BrandMark = {
  Mark: ComponentType<{ size?: number }>
  /** A CSS background -- flat for most brands, a gradient for five. Null when
   *  the brand's tile is white: use the neutral tile instead. */
  background: string | null
  /** Null alongside a null background: the neutral tile draws in the
   *  foreground token. */
  color: string | null
}

export const BRAND_MARKS: Record<string, BrandMark> = {
  "ai21": { Mark: Ai21Mono, background: "#E91E63", color: "#fff" },
  "alibaba": { Mark: AlibabaMono, background: "#FF6003", color: "#fff" },
  "anthropic": { Mark: AnthropicMono, background: "#F1F0E8", color: "#141413" },
  "anthropic-oauth": { Mark: AnthropicMono, background: "#F1F0E8", color: "#141413" },
  "baichuan": { Mark: BaichuanMono, background: "#FF6933", color: "#fff" },
  "baidu": { Mark: BaiduMono, background: "#2932E1", color: "#fff" },
  "bailian-coding-plan": { Mark: BailianMono, background: null, color: null },
  "baseten": { Mark: BasetenMono, background: "#19E76E", color: "#000" },
  "bedrock": { Mark: BedrockMono, background: "linear-gradient(45deg, #9AD8F8, #3D8FFF, #6350FB)", color: "#fff" },
  "byteplus": { Mark: ByteDanceMono, background: "#325AB4", color: "#fff" },
  "cerebras": { Mark: CerebrasMono, background: "#F15A29", color: "#fff" },
  "cloudflare-ai": { Mark: CloudflareMono, background: "#F38020", color: "#fff" },
  "cloudflare-playground": { Mark: CloudflareMono, background: "#F38020", color: "#fff" },
  "codestral": { Mark: MistralMono, background: "#FA520F", color: "#fff" },
  "cohere": { Mark: CohereMono, background: "#39594D", color: "#fff" },
  "coze": { Mark: CozeMono, background: "#4D53E8", color: "#fff" },
  "databricks": { Mark: DbrxMono, background: "#EE3D2C", color: "#fff" },
  "deepinfra": { Mark: DeepInfraMono, background: null, color: null },
  "deepseek": { Mark: DeepSeekMono, background: "#4D6BFE", color: "#fff" },
  "dify": { Mark: DifyMono, background: null, color: null },
  "doubao": { Mark: DoubaoMono, background: null, color: null },
  "featherless-ai": { Mark: FeatherlessMono, background: "#FFE184", color: "#000" },
  "fireworks": { Mark: FireworksMono, background: "#5019C5", color: "#000" },
  "friendliai": { Mark: FriendliMono, background: null, color: null },
  "g4f-gemini": { Mark: GeminiMono, background: null, color: null },
  "g4f-groq": { Mark: GroqMono, background: "#F55036", color: "#fff" },
  "g4f-nvidia": { Mark: NvidiaMono, background: "#74B71B", color: "#fff" },
  "g4f-ollama": { Mark: OllamaMono, background: null, color: null },
  "g4f-pollinations": { Mark: PollinationsMono, background: "#000", color: "#fff" },
  "gemini": { Mark: GeminiMono, background: null, color: null },
  "glm": { Mark: ChatGLMMono, background: "#4268FA", color: "#fff" },
  "groq": { Mark: GroqMono, background: "#F55036", color: "#fff" },
  "haiper": { Mark: HaiperMono, background: "#9581ff", color: "#000" },
  "huggingface": { Mark: HuggingFaceMono, background: null, color: null },
  "hyperbolic": { Mark: HyperbolicMono, background: "#594CE9", color: "#fff" },
  "ideogram": { Mark: IdeogramMono, background: null, color: null },
  "iflytek": { Mark: SparkMono, background: "#0070f0", color: "#fff" },
  "inception": { Mark: InceptionMono, background: null, color: null },
  "inference-net": { Mark: InferenceMono, background: "#000", color: "#fff" },
  "internlm": { Mark: InternLMMono, background: "#1B3882", color: "#fff" },
  "kimi": { Mark: KimiMono, background: "#000", color: "#fff" },
  "kimi-k3": { Mark: KimiMono, background: "#000", color: "#fff" },
  "lambda-ai": { Mark: LambdaMono, background: "#000", color: "#fff" },
  "liquid": { Mark: LiquidMono, background: null, color: null },
  "lmstudio": { Mark: LmStudioMono, background: "linear-gradient(135deg, #6C78EF, #4F14BE)", color: "#fff" },
  "longcat": { Mark: LongCatMono, background: null, color: null },
  "meta-llama": { Mark: MetaMono, background: "linear-gradient(45deg, #007FF8, #0668E1, #007FF8)", color: "#fff" },
  "minimax": { Mark: MinimaxMono, background: "linear-gradient(to right, #E2167E,  #FE603C)", color: "#fff" },
  "mistral": { Mark: MistralMono, background: "#FA520F", color: "#fff" },
  "modelscope": { Mark: ModelScopeMono, background: "#624AFF", color: "#fff" },
  "morph": { Mark: MorphMono, background: "#000", color: "#99d52a" },
  "nebius": { Mark: NebiusMono, background: "#DAFF33", color: "#052b42" },
  "nous-research": { Mark: NousResearchMono, background: null, color: null },
  "novita": { Mark: NovitaMono, background: "#23D57C", color: "#000" },
  "nvidia": { Mark: NvidiaMono, background: "#74B71B", color: "#fff" },
  "ollama": { Mark: OllamaMono, background: null, color: null },
  "ollama-cloud": { Mark: OllamaMono, background: null, color: null },
  "openai": { Mark: OpenAIMono, background: "#000", color: "#fff" },
  "opencode": { Mark: OpenCodeMono, background: "#000", color: "#fff" },
  "openrouter": { Mark: OpenRouterMono, background: "#000", color: "#C8FF00" },
  "perplexity": { Mark: PerplexityMono, background: "#22B8CD", color: "#000" },
  "pollinations": { Mark: PollinationsMono, background: "#000", color: "#fff" },
  "poolside": { Mark: PoolsideMono, background: "#4137FF", color: "#fff" },
  "qiniu": { Mark: QiniuMono, background: "#06AEEF", color: "#fff" },
  "qoder": { Mark: QoderMono, background: "#000000", color: "#fff" },
  "qwen-cloud": { Mark: QwenMono, background: "#615ced", color: "#fff" },
  "sambanova": { Mark: SambaNovaMono, background: "#EE7624", color: "#fff" },
  "sensenova": { Mark: SenseNovaMono, background: "#5B2AD8", color: "#fff" },
  "snowflake": { Mark: SnowflakeMono, background: "#249EDC", color: "#fff" },
  "stepfun": { Mark: StepfunMono, background: null, color: null },
  "tencent": { Mark: TencentMono, background: "#0052D9", color: "#fff" },
  "together": { Mark: TogetherMono, background: null, color: null },
  "udio": { Mark: UdioMono, background: "#e30a5d", color: "#fff" },
  "upstage": { Mark: UpstageMono, background: "linear-gradient(to bottom, #AEBCFE,  #805DFA)", color: "#fff" },
  "v0-vercel": { Mark: V0Mono, background: "#000", color: "#fff" },
  "venice": { Mark: VeniceMono, background: "#E05A2D", color: "#fff" },
  "vercel-ai-gateway": { Mark: VercelMono, background: "#000", color: "#fff" },
  "vertex": { Mark: VertexAIMono, background: "#4285F4", color: "#fff" },
  "vertex-anthropic": { Mark: VertexAIMono, background: "#4285F4", color: "#fff" },
  "volcengine": { Mark: VolcengineMono, background: null, color: null },
  "voyage": { Mark: VoyageMono, background: "#012E33", color: "#fff" },
  "xai": { Mark: XAIMono, background: null, color: null },
  "xiaomi-mimo": { Mark: XiaomiMiMoMono, background: "#000", color: "#fff" },
  "xiaomi-mimo-token-plan": { Mark: XiaomiMiMoMono, background: "#000", color: "#fff" },
  "yi": { Mark: YiMono, background: "#003425", color: "#fff" },
  "zai": { Mark: ZAIMono, background: "#000", color: "#fff" },
  "zenmux": { Mark: ZenMuxMono, background: "#000", color: "#fff" },
}
