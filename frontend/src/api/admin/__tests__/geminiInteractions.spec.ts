import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))

import { geminiInteractionsAPI } from '../geminiInteractions'

describe('Gemini Interactions 人工核对 API', () => {
  beforeEach(() => vi.resetAllMocks())

  it('传递分页并返回待核对列表', async () => {
    const page = { items: [], total: 0, page: 2, page_size: 20 }
    get.mockResolvedValue({ data: page })
    await expect(geminiInteractionsAPI.list({ page: 2, page_size: 20 })).resolves.toEqual(page)
    expect(get).toHaveBeenCalledWith('/admin/balance-center/interactions', { params: { page: 2, page_size: 20 } })
  })

  it('原样提交已核实用量，不提交手工费用且保留等待重试结果', async () => {
    const payload = { action: 'settle' as const, reason: '已在上游账单核实用量', usage: { input_tokens: 100, output_tokens: 20, cache_read_input_tokens: 5, image_output_tokens: 6 } }
    post.mockResolvedValue({ data: { settled: false } })
    await expect(geminiInteractionsAPI.resolve(7, payload)).resolves.toEqual({ settled: false })
    expect(post).toHaveBeenCalledWith('/admin/balance-center/interactions/7/resolve', payload)
  })

  it('解冻只提交动作和核对理由', async () => {
    const payload = { action: 'release' as const, reason: '已确认上游未执行且无需收费' }
    post.mockResolvedValue({ data: { settled: true } })
    await expect(geminiInteractionsAPI.resolve(7, payload)).resolves.toEqual({ settled: true })
    expect(post).toHaveBeenCalledWith('/admin/balance-center/interactions/7/resolve', payload)
  })
})
