import type { Account } from '@/types'

export interface MockAccount extends Account {
  credentials: { email: string; plan_type: string; subscription_expires_at: string }
  extra: { privacy_mode: string; openai_compact_mode: 'auto' }
  usage: { fiveHour: number; sevenDay: number; fiveHourReset: string; sevenDayReset: string }
}

const firstNames = ['genesis', 'amani', 'maudie', 'neoma', 'theo', 'margarete', 'ryan', 'carlotta', 'elena', 'julian', 'nora', 'lucas', 'sophie', 'miles', 'clara', 'ethan']
const lastNames = ['bennett', 'morgan', 'ellis', 'hayes', 'parker', 'reid', 'sullivan', 'brooks', 'foster', 'collins', 'walsh', 'miller', 'santos', 'martin', 'clarke', 'ross']
const domains = ['gmail.com', 'gmail.com', 'outlook.com', 'mail.com']
const plans = ['self_serve_business_prolite', 'self_serve_business_prolite', 'team', 'pro', 'plus']
const integer = (min: number, max: number) => min + Math.floor(Math.random() * (max - min + 1))
const pick = <T>(values: T[]): T => values[integer(0, values.length - 1)]!

// 仅生成页面内存数据，沿用真实账号字段，不包含凭据或请求接口。
export function generateMockAccounts(count = 300, now = Date.now()): MockAccount[] {
  const day = 86_400_000
  let id = integer(20300, 20900) + count * 3
  return Array.from({ length: count }, (_, index) => {
    id -= integer(1, 3)
    const email = `${pick(firstNames)}${pick(['.', '_', ''])}${pick(lastNames)}${integer(10, 99)}${index.toString(36)}@${pick(domains)}`
    const createdAt = new Date(now - integer(3, 90) * day - integer(0, day - 1)).toISOString()
    const subscriptionExpiresAt = new Date(now + integer(1, 30) * day).toISOString()
    const fiveHour = integer(12, 84)
    return {
      id, name: email, platform: 'openai', type: 'oauth',
      credentials: { email, plan_type: pick(plans), subscription_expires_at: subscriptionExpiresAt },
      extra: { privacy_mode: 'training_off', openai_compact_mode: 'auto' },
      concurrency: 20, current_concurrency: integer(10, 20),
      status: 'active', schedulable: true, priority: 50, rate_multiplier: 1,
      proxy_id: null, error_message: null, group_ids: [], groups: [],
      created_at: createdAt, updated_at: createdAt,
      last_used_at: new Date(now - integer(1, 45) * 1000).toISOString(),
      expires_at: null, auto_pause_on_expired: true,
      rate_limited_at: null, rate_limit_reset_at: null, overload_until: null,
      temp_unschedulable_until: null, temp_unschedulable_reason: null,
      session_window_start: null, session_window_end: null, session_window_status: null,
      usage: {
        fiveHour, sevenDay: integer(8, 78),
        fiveHourReset: new Date(now + integer(15, 295) * 60_000).toISOString(),
        sevenDayReset: new Date(now + integer(6, 167) * 3_600_000).toISOString()
      }
    }
  })
}
