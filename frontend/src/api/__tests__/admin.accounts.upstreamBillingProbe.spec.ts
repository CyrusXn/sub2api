import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post, put }
}))

import {
  getUpstreamBillingProbeSettings,
  probeUpstreamBilling,
  probeUpstreamBillingBatch,
  refreshBusinessBalances,
  setUpstreamBillingProbeEnabled,
  updateUpstreamBillingProbeSettings
} from '@/api/admin/accounts'

describe('admin account upstream billing probe API', () => {
  beforeEach(() => {
    vi.unstubAllEnvs()
    get.mockReset()
    post.mockReset()
    put.mockReset()
  })

  it('reads and updates global settings', async () => {
    const settings = { enabled: true, interval_minutes: 30 }
    get.mockResolvedValueOnce({ data: settings })
    put.mockResolvedValueOnce({ data: settings })

    await expect(getUpstreamBillingProbeSettings()).resolves.toEqual(settings)
    await expect(updateUpstreamBillingProbeSettings(settings)).resolves.toEqual(settings)
    expect(get).toHaveBeenCalledWith('/admin/accounts/upstream-billing-probe/settings')
    expect(put).toHaveBeenCalledWith('/admin/accounts/upstream-billing-probe/settings', settings)
  })

  it('uses dedicated account and batch endpoints', async () => {
    const result = { account_id: 7, snapshot: { status: 'unsupported' } }
    put.mockResolvedValueOnce({ data: {} })
    post.mockResolvedValueOnce({ data: result })
    post.mockResolvedValueOnce({ data: { results: [result] } })

    await setUpstreamBillingProbeEnabled(7, true)
    await expect(probeUpstreamBilling(7)).resolves.toEqual(result)
    await expect(probeUpstreamBillingBatch([7])).resolves.toEqual([result])

    expect(put).toHaveBeenCalledWith('/admin/accounts/7/upstream-billing-probe', { enabled: true })
    expect(post).toHaveBeenNthCalledWith(1, '/admin/accounts/7/upstream-billing-probe')
    expect(post).toHaveBeenNthCalledWith(2, '/admin/accounts/upstream-billing-probe/batch', { account_ids: [7] })
  })

  it('实时余额跨页读取账号，按域名取本轮最小值且保留零余额', async () => {
    get.mockResolvedValueOnce({ data: { items: [
      { id: 1, type: 'apikey', credentials: { base_url: 'https://UP.example/v1' } },
      { id: 2, type: 'apikey', credentials: { base_url: 'https://up.example/' } },
      { id: 9, type: 'apikey', credentials: { base_url: 'https://api.openai.com/v1' } }
    ], total: 4 } }).mockResolvedValueOnce({ data: { items: [
      { id: 3, type: 'apikey', credentials: { base_url: 'https://other.example' } }
    ], total: 4 } })
    post.mockResolvedValueOnce({ data: { results: [
      { account_id: 1, snapshot: { balance: { status: 'ok', amount: 328.22 } } },
      { account_id: 2, snapshot: { balance: { status: 'ok', amount: 328 } } },
      { account_id: 3, snapshot: { balance: { status: 'ok', amount: 0 } } }
    ] } })
    const result = await refreshBusinessBalances()
    expect(result.total_balance).toBe(328)
    expect(result.sites).toHaveLength(2)
    expect(result.failed_accounts).toBe(0)
    expect(get).toHaveBeenCalledTimes(2)
    expect(post.mock.calls[0][1].account_ids).toEqual([1, 2, 3])
  })

  it('余额获取失败的账号按零计入，不阻断其他上游汇总', async () => {
    get.mockResolvedValue({ data: { items: [
      { id: 1, type: 'apikey', credentials: { base_url: 'https://failed.example' } },
      { id: 2, type: 'apikey', credentials: { base_url: 'https://up.example' } }
    ], total: 2 } })
    post.mockResolvedValue({ data: { results: [
      { account_id: 1, error: '余额接口不可用' },
      { account_id: 2, snapshot: { balance: { status: 'ok', amount: 18 } } }
    ] } })
    const result = await refreshBusinessBalances()
    expect(result.total_balance).toBe(18)
    expect(result.sites).toEqual([
      { domain: 'failed.example', balance: 0, failed_accounts: 1 },
      { domain: 'up.example', balance: 18, failed_accounts: 0 }
    ])
    expect(result.failed_accounts).toBe(1)
  })

  it.each([false, true])('同一上游失败账号按零取最小值，不受账号顺序影响（倒序：%s）', async reverse => {
    const items = [1, 2].map(id => ({ id, type: 'apikey', credentials: { base_url: 'https://up.example' } }))
    get.mockResolvedValue({ data: { items: reverse ? items.reverse() : items, total: 2 } })
    post.mockResolvedValue({ data: { results: [
      { account_id: 1, snapshot: { balance: { status: 'ok', amount: 18 } } },
      { account_id: 2, error: '余额获取失败' }
    ] } })
    const result = await refreshBusinessBalances()
    expect(result.total_balance).toBe(0)
    expect(result.sites).toEqual([{ domain: 'up.example', balance: 0, failed_accounts: 1 }])
  })

  it.each(['missing', 'invalid', 'rejected'])('探测结果缺失、无效或请求失败按零计算：%s', async mode => {
    get.mockResolvedValue({ data: { items: [
      { id: 1, type: 'apikey', credentials: { base_url: 'https://up.example' } }
    ], total: 1 } })
    if (mode === 'rejected') post.mockRejectedValue(new Error('探测失败'))
    else post.mockResolvedValue({ data: { results: mode === 'missing' ? [] : [
      { account_id: 1, snapshot: { balance: { status: 'ok', amount: '18' } } }
    ] } })
    const result = await refreshBusinessBalances()
    expect(result.total_balance).toBe(0)
    expect(result.failed_accounts).toBe(1)
    expect(result.sites[0].balance).toBe(0)
  })

  it('本地只读验收仅汇总线上已有余额，不触发探测写入', async () => {
    vi.stubEnv('VITE_READ_ONLY_PREVIEW', 'true')
    get.mockResolvedValue({ data: { items: [
      { id: 1, type: 'apikey', credentials: { base_url: 'https://up.example' }, extra: { upstream_billing_probe: { balance: { status: 'ok', amount: 10, received_at: '2026-10-22T01:00:00Z' } } } }
    ], total: 1 } })
    const result = await refreshBusinessBalances()
    expect(result.total_balance).toBe(10)
    expect(result.preview_only).toBe(true)
    expect(result.refreshed_at).toBe('2026-10-22T01:00:00Z')
    expect(post).not.toHaveBeenCalled()
    vi.unstubAllEnvs()
  })
})
