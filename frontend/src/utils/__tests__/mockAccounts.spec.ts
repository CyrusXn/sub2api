import { afterEach, describe, expect, it, vi } from 'vitest'
import { generateMockAccounts } from '../mockAccounts'

afterEach(() => vi.restoreAllMocks())

describe('OpenAI 模拟账号', () => {
  it.each([0, 0.5, 0.999999])('随机边界 %s 下保持并发、日期与账号契约', (random) => {
    vi.spyOn(Math, 'random').mockReturnValue(random)
    const now = Date.parse('2026-09-17T08:00:00Z')
    const accounts = generateMockAccounts(300, now)
    expect(accounts).toHaveLength(300)
    expect(new Set(accounts.map((account) => account.id)).size).toBe(300)
    expect(new Set(accounts.map((account) => account.credentials.email)).size).toBe(300)
    for (const account of accounts) {
      expect(account.platform).toBe('openai')
      expect(account.type).toBe('oauth')
      expect(account.extra.privacy_mode).toBe('training_off')
      expect(account.concurrency).toBe(20)
      expect(account.current_concurrency).toBeGreaterThanOrEqual(10)
      expect(account.current_concurrency).toBeLessThanOrEqual(20)
      expect(account.status).toBe('active')
      expect(Date.parse(account.created_at)).toBeLessThan(Date.parse(account.last_used_at!))
      expect(Date.parse(account.last_used_at!)).toBeLessThanOrEqual(now)
      expect(Date.parse(account.credentials.subscription_expires_at)).toBeGreaterThan(now)
      expect(Date.parse(account.usage.fiveHourReset)).toBeGreaterThan(now)
      expect(Date.parse(account.usage.fiveHourReset)).toBeLessThanOrEqual(now + 5 * 3_600_000)
      expect(Date.parse(account.usage.sevenDayReset)).toBeLessThanOrEqual(now + 7 * 86_400_000)
      expect(account.credentials).not.toHaveProperty('access_token')
      expect(account.credentials).not.toHaveProperty('refresh_token')
    }
  })
})
