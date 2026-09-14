/**
 * Channel monitor shared constants.
 *
 * Single source of truth for provider/status string values used by both the
 * admin (`views/admin/ChannelMonitorView.vue`) and user-facing
 * (`views/user/ChannelStatusView.vue`) screens, plus the shared composable
 * `useChannelMonitorFormat`.
 */

import type { APIMode, CheckMode, Provider, MonitorStatus } from '@/api/admin/channelMonitor'

export const PROVIDER_OPENAI: Provider = 'openai'
export const PROVIDER_ANTHROPIC: Provider = 'anthropic'
export const PROVIDER_GEMINI: Provider = 'gemini'
export const PROVIDER_GROK: Provider = 'grok'
export const PROVIDER_ANTIGRAVITY: Provider = 'antigravity'
export const PROVIDER_KIMI: Provider = 'kimi'
export const PROVIDER_ZHIPU: Provider = 'zhipu'
export const PROVIDER_DEEPSEEK: Provider = 'deepseek'
export const PROVIDER_MINIMAX: Provider = 'minimax'
export const PROVIDER_OPENCODE_GO: Provider = 'opencode_go'

export const DEFAULT_GROK_ENDPOINT = 'https://api.x.ai'
export const DEFAULT_GROK_MODEL = 'grok-4.5'

/** 国产 provider 的官方 endpoint（探活模式预填；配额模式可留空）。 */
export const DEFAULT_KIMI_ENDPOINT = 'https://api.moonshot.cn'
export const DEFAULT_ZHIPU_ENDPOINT = 'https://open.bigmodel.cn'
export const DEFAULT_DEEPSEEK_ENDPOINT = 'https://api.deepseek.com'
export const DEFAULT_MINIMAX_ENDPOINT = 'https://api.minimaxi.com'
export const DEFAULT_OPENCODE_GO_ENDPOINT = 'https://opencode.ai/zen/go/v1'

export const CHECK_MODE_PROBE: CheckMode = 'probe'
export const CHECK_MODE_QUOTA: CheckMode = 'quota'
export const CHECK_MODE_QUOTA_PROBE: CheckMode = 'quota_probe'

export const API_MODE_CHAT_COMPLETIONS: APIMode = 'chat_completions'
export const API_MODE_RESPONSES: APIMode = 'responses'

export const PROVIDERS: readonly Provider[] = [
  PROVIDER_OPENAI,
  PROVIDER_ANTHROPIC,
  PROVIDER_GEMINI,
  PROVIDER_GROK,
  PROVIDER_ANTIGRAVITY,
  PROVIDER_KIMI,
  PROVIDER_ZHIPU,
  PROVIDER_DEEPSEEK,
  PROVIDER_MINIMAX,
  PROVIDER_OPENCODE_GO,
]

/** 仅支持配额模式（无探活 adapter）的 provider。 */
export const QUOTA_ONLY_PROVIDERS: readonly Provider[] = [PROVIDER_ANTIGRAVITY]

export const CHECK_MODES: readonly CheckMode[] = [
  CHECK_MODE_PROBE,
  CHECK_MODE_QUOTA,
  CHECK_MODE_QUOTA_PROBE,
]

export const API_MODES: readonly APIMode[] = [
  API_MODE_CHAT_COMPLETIONS,
  API_MODE_RESPONSES,
]

export const STATUS_OPERATIONAL: MonitorStatus = 'operational'
export const STATUS_DEGRADED: MonitorStatus = 'degraded'
export const STATUS_FAILED: MonitorStatus = 'failed'
export const STATUS_ERROR: MonitorStatus = 'error'

export const MONITOR_STATUSES: readonly MonitorStatus[] = [
  STATUS_OPERATIONAL,
  STATUS_DEGRADED,
  STATUS_FAILED,
  STATUS_ERROR,
]

/** Default polling interval (seconds) for new monitors. */
export const DEFAULT_INTERVAL_SECONDS = 60

/**
 * 渠道状态页平台分组键。
 *
 * 用户要求渠道状态按平台分组展示，固定顺序为 OpenAI → Claude → Grok → 国模，
 * 其余平台依次追加。这里用固定枚举 + 固定顺序表达，渲染时过滤掉空分组，
 * 避免引入动态分组注册表（YAGNI）。
 */
export type MonitorGroupKey =
  | 'openai'
  | 'claude'
  | 'grok'
  | 'cn'
  | 'gemini'
  | 'antigravity'
  | 'other'

/** 分组展示顺序；空分组不渲染。 */
export const MONITOR_GROUP_ORDER: readonly MonitorGroupKey[] = [
  'openai',
  'claude',
  'grok',
  'cn',
  'gemini',
  'antigravity',
  'other',
]

/** provider → 分组的静态映射；未列出的 provider 落入 other。 */
const PROVIDER_GROUP_MAP: Readonly<Record<string, MonitorGroupKey>> = {
  [PROVIDER_OPENAI]: 'openai',
  [PROVIDER_ANTHROPIC]: 'claude',
  [PROVIDER_GROK]: 'grok',
  [PROVIDER_KIMI]: 'cn',
  [PROVIDER_ZHIPU]: 'cn',
  [PROVIDER_DEEPSEEK]: 'cn',
  [PROVIDER_GEMINI]: 'gemini',
  [PROVIDER_ANTIGRAVITY]: 'antigravity',
}

/**
 * 国模关键词兜底表。
 *
 * 部分国产模型渠道走的是 OpenAI 兼容协议，后台 provider 只能填 openai
 * （改成 kimi/zhipu 会改变探活路径并丢失 responses 模式），但实际提供的是国产模型，
 * 例如渠道名「国模」、主模型 `k3`。这里用渠道名 + 主模型名关键词把它们纠正到国模分组。
 */
const CN_MODEL_KEYWORDS: readonly string[] = [
  '国模',
  '国产',
  'kimi',
  'moonshot',
  'k2',
  'k3',
  'glm',
  'zhipu',
  'chatglm',
  '智谱',
  'deepseek',
  'qwen',
  'tongyi',
  '通义',
  'doubao',
  '豆包',
  'hunyuan',
  '混元',
  'ernie',
  '文心',
  'minimax',
  '星火',
  'spark',
]

/** 渠道分组判定所需的最小字段集合（避免依赖完整的 UserMonitorView 类型）。 */
export interface MonitorGroupInput {
  provider?: string | null
  name?: string | null
  primary_model?: string | null
}

/** 文本是否命中国模关键词（大小写不敏感）。 */
function matchesCNKeyword(text: string): boolean {
  const lower = text.toLowerCase()
  return CN_MODEL_KEYWORDS.some(keyword => lower.includes(keyword.toLowerCase()))
}

/**
 * 判定渠道所属平台分组。
 *
 * 先按 provider 静态映射；只有当结果是 openai 或 other（即 provider 本身无法区分国产模型）时，
 * 才用渠道名 / 主模型名做国模关键词兜底，避免误把 anthropic、grok 等明确平台的渠道搬走。
 */
export function providerGroupOf(item: MonitorGroupInput): MonitorGroupKey {
  const provider = (item.provider || '').trim().toLowerCase()
  const group = PROVIDER_GROUP_MAP[provider] ?? 'other'
  if (group !== 'openai' && group !== 'other') {
    return group
  }
  const hint = `${item.name || ''} ${item.primary_model || ''}`.trim()
  if (hint && matchesCNKeyword(hint)) {
    return 'cn'
  }
  return group
}
