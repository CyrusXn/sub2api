import { describe, expect, it } from 'vitest'
import type { Account } from '@/types'
import { upstreamSubscriptionRate } from '../upstreamSubscriptionRate'

describe('订阅倍率展示', () => {
  const now = Date.parse('2026-09-16T00:01:00Z')
  const account = () => ({ extra: {
    upstream_billing_probe_enabled: true,
    upstream_subscription_rate: { enabled: true, subscription_id: 944, key_fingerprint: 'test-fingerprint', price: 808, quota_usd: 30000, group_multiplier: 4 },
    upstream_billing_probe: { subscription: { key_fingerprint: 'test-fingerprint', subscription_id: 944, rate: 808 / 30000 * 4, remaining_usd: 50, observed_at: '2026-09-16T00:00:00Z', fresh_until: '2026-09-16T00:02:00Z', expires_at: '2026-09-17T00:00:00Z' } }
  } } as unknown as Account)
  it('显示完整计算精度，关闭探测或额度耗尽后回退', () => {
    const a = account()
    expect(upstreamSubscriptionRate(a, now)).toBe(808 / 30000 * 4)
    a.extra!.upstream_billing_probe_enabled = false
    expect(upstreamSubscriptionRate(a, now)).toBeNull()
    a.extra!.upstream_billing_probe_enabled = true
    a.extra!.upstream_billing_probe!.subscription!.remaining_usd = 0
    expect(upstreamSubscriptionRate(a, now)).toBeNull()
  })
  it('过期和不同Key观察不会被显示成生效订阅', () => {
    const a = account()
    expect(upstreamSubscriptionRate(a, now + 120000)).toBeNull()
    a.extra!.upstream_billing_probe!.subscription!.key_fingerprint = 'another-key'
    expect(upstreamSubscriptionRate(a, now)).toBeNull()
  })
})
