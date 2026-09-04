export const FREE_CATALOG_CURATED_AT = "2026-08-30";
export const FREE_MODEL_BUDGETS = [
  { provider: "api-airforce", modelId: "x-ai/grok-3", displayName: "Grok-3 (Free)", monthlyTokens: 24000000, creditTokens: 0, freeType: "recurring-daily", poolKey: "api-airforce", tos: "caution" },
  { provider: "api-airforce", modelId: "x-ai/grok-2-1212", displayName: "Grok-2 1212 (Free)", monthlyTokens: 24000000, creditTokens: 0, freeType: "recurring-daily", poolKey: "api-airforce", tos: "caution" },
  { provider: "agentrouter", modelId: "claude-opus-4-8", displayName: "Claude Opus 4.8", monthlyTokens: 0, creditTokens: 200000000, freeType: "one-time-initial", poolKey: "agentrouter", tos: "caution" },
  { provider: "agentrouter", modelId: "claude-opus-5", displayName: "Claude Opus 5", monthlyTokens: 0, creditTokens: 200000000, freeType: "one-time-initial", poolKey: "agentrouter", tos: "caution" },
  { provider: "agy", modelId: "gemini-3.7-flash-high", displayName: "Gemini 3.7 Flash (High)", monthlyTokens: 0, creditTokens: 0, freeType: "keyless", poolKey: "agy", tos: "avoid" },
  { provider: "agy", modelId: "gemini-3.7-flash-medium", displayName: "Gemini 3.7 Flash (Medium)", monthlyTokens: 0, creditTokens: 0, freeType: "keyless", poolKey: "agy", tos: "avoid" },
  { provider: "baidu", modelId: "ernie-4.0-8k", displayName: "ERNIE 4.0 8K", monthlyTokens: 0, creditTokens: 0, freeType: "recurring-uncapped", poolKey: "baidu", tos: "caution" },
  { provider: "glm-cn", modelId: "glm-4-flash", displayName: "GLM-4-Flash", monthlyTokens: 0, creditTokens: 0, freeType: "recurring-uncapped", poolKey: "zhipu-flash-free", tos: "ok" },
  { provider: "cohere", modelId: "command-a-reasoning-08-2025", displayName: "Command A Reasoning (Aug 2025)", monthlyTokens: 800000, creditTokens: 0, freeType: "recurring-monthly", poolKey: "cohere", tos: "caution" },
  { provider: "bytez", modelId: "meta-llama/Llama-3.3-70B-Instruct", displayName: "meta-llama/Llama-3.3-70B-Instruct", monthlyTokens: 0, creditTokens: 1000000, freeType: "recurring-credit", poolKey: "bytez", tos: "ambiguous" },
  { provider: "pollinations", modelId: "gemini", displayName: "Gemini (Pollinations) — requires API key", monthlyTokens: 0, creditTokens: 0, freeType: "discontinued", poolKey: null, tos: "caution" },
  { provider: "deepseek", modelId: "deepseek-v4-pro", displayName: "DeepSeek V4 Pro", monthlyTokens: 0, creditTokens: 5000000, freeType: "one-time-initial", poolKey: "deepseek", tos: "ok" },
  { provider: "freemodel-dev", modelId: "gpt-5.5", displayName: "GPT-5.5", monthlyTokens: 0, creditTokens: 0, freeType: "one-time-initial", poolKey: "freemodel-dev", tos: "unknown" },
]
