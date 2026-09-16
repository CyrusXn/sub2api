import type { Account } from '@/types'

export function upstreamSubscriptionRate(account: Account, now = Date.now()): number | null {
  const config = account.extra?.upstream_subscription_rate as {
    enabled?: boolean; subscription_id?: number; key_fingerprint?: string
    price?: number; quota_usd?: number; group_multiplier?: number
  } | undefined
  const observation = account.extra?.upstream_billing_probe?.subscription
  if (account.extra?.upstream_billing_probe_enabled !== true || !config?.enabled || !observation || observation.error || !config.key_fingerprint || config.key_fingerprint !== observation.key_fingerprint || config.subscription_id !== observation.subscription_id) return null
  const rate = Number(config.price) / Number(config.quota_usd) * Number(config.group_multiplier)
  if (!Number.isFinite(rate) || rate <= 0 || rate !== observation.rate || !Number.isFinite(Number(observation.remaining_usd)) || !(Number(observation.remaining_usd) > 0)) return null
  if (!(now >= Date.parse(observation.observed_at) && now < Date.parse(observation.fresh_until) && now < Date.parse(observation.expires_at))) return null
  return rate
}
