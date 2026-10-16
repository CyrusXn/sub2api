import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import GeminiInteractionReview from '../GeminiInteractionReview.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), resolve: vi.fn() }))
vi.mock('@/api/admin/geminiInteractions', () => ({ geminiInteractionsAPI: api }))
enableAutoUnmount(afterEach)

const item = {
  id: 7, public_id: 'local-review-7', user_id: 10, api_key_id: 11, group_id: 12, account_id: 13,
  model: 'gemini-3-flash-preview', status: 'requires_review', hold_amount: 0.25,
  review_reason: 'terminal_usage_missing', created_at: '2026-10-16T10:00:00Z',
  upstream_id: 'upstream-secret-identifier', credentials: 'never-display-this'
}

function page(total = 1, current = 1) { return { items: [item], total, page: current, page_size: 20 } }
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}
async function open() {
  const wrapper = mount(GeminiInteractionReview, { global: { stubs: { teleport: true, transition: false } } })
  await flushPromises()
  return wrapper
}
type Wrapper = Awaited<ReturnType<typeof open>>
async function fillUsage(wrapper: Wrapper) {
  await wrapper.get('[data-test="review-input-tokens"]').setValue('100')
  await wrapper.get('[data-test="review-output-tokens"]').setValue('20')
  await wrapper.get('[data-test="review-cache-tokens"]').setValue('5')
  await wrapper.get('[data-test="review-image-tokens"]').setValue('6')
  await wrapper.get('[data-test="review-reason"]').setValue('已核实上游最终账单')
}

describe('Gemini Interactions 账务待核对', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.list.mockResolvedValue(page())
    api.resolve.mockResolvedValue({ settled: true })
  })

  it('显示关联信息、冻结金额及中文核对原因，不显示上游标识和凭据', async () => {
    const wrapper = await open()
    expect(api.list).toHaveBeenCalledWith({ page: 1, page_size: 20 })
    expect(wrapper.text()).toContain('Gemini Interactions 账务待核对')
    expect(wrapper.text()).toContain('local-review-7')
    expect(wrapper.text()).toContain('用户 #10')
    expect(wrapper.text()).toContain('密钥 #11')
    expect(wrapper.text()).toContain('分组 #12')
    expect(wrapper.text()).toContain('账号 #13')
    expect(wrapper.text()).toContain('$0.250000')
    expect(wrapper.text()).toContain('请求已结束但缺少用量，需核实实际计费')
    expect(wrapper.text()).not.toContain(item.upstream_id)
    expect(wrapper.text()).not.toContain(item.credentials)
  })

  it('支持分页和当前页刷新', async () => {
    api.list.mockResolvedValueOnce(page(21)).mockResolvedValue(page(21, 2))
    const wrapper = await open()
    await wrapper.get('[data-test="review-next"]').trigger('click')
    await flushPromises()
    expect(api.list).toHaveBeenLastCalledWith({ page: 2, page_size: 20 })
    await wrapper.get('[data-test="review-refresh"]').trigger('click')
    await flushPromises()
    expect(api.list).toHaveBeenLastCalledWith({ page: 2, page_size: 20 })
  })

  it('取消核对不提交任何账务操作', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-release-7"]').trigger('click')
    await wrapper.get('[data-test="review-cancel"]').trigger('click')
    expect(api.resolve).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="review-form"]').exists()).toBe(false)
  })

  it('人工用量说明与现有Google解析契约一致，避免缓存和思考重复计费', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    expect(wrapper.text()).toContain('非缓存输入 Token')
    expect(wrapper.text()).toContain('总输出 Token（含思考）')
    expect(wrapper.text()).toContain('缓存读取 Token')
    expect(wrapper.text()).toContain('图片输出 Token（总输出子集）')
    expect(wrapper.text()).toContain('total_input_tokens 需减去 total_cached_tokens')
    expect(wrapper.text()).toContain('total_output_tokens 需加上 total_thought_tokens')
    expect(wrapper.text()).toContain('图片输出已包含在总输出内，不要再次相加，避免重复计费')
  })

  it('图片输出用量必须显式填写且为不超过总输出的非负整数', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    await fillUsage(wrapper)
    expect(wrapper.find('[data-test="review-image-tokens"]').exists()).toBe(true)
    for (const value of ['', '-1', '0.5', '21']) {
      await wrapper.get('[data-test="review-image-tokens"]').setValue(value)
      await wrapper.get('[data-test="review-form"]').trigger('submit')
      expect(api.resolve).not.toHaveBeenCalled()
    }
    expect(wrapper.text()).toContain('图片输出 Token 不能超过总输出 Token')
  })

  it.each(['0', '20'])('明确提交图片输出子集%s，不从总输出再次扣除', async value => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    await fillUsage(wrapper)
    expect(wrapper.find('[data-test="review-image-tokens"]').exists()).toBe(true)
    await wrapper.get('[data-test="review-image-tokens"]').setValue(value)
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    expect(api.resolve).toHaveBeenCalledWith(7, {
      action: 'settle', reason: '已核实上游最终账单',
      usage: { input_tokens: 100, output_tokens: 20, cache_read_input_tokens: 5, image_output_tokens: Number(value) }
    })
  })

  it('理由和人工核实的整数用量均必填，并且不接受负数或小数', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    expect(api.resolve).not.toHaveBeenCalled()
    await fillUsage(wrapper)
    await wrapper.get('[data-test="review-reason"]').setValue('太短')
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    expect(api.resolve).not.toHaveBeenCalled()
    await wrapper.get('[data-test="review-reason"]').setValue('已核实上游最终账单')
    for (const value of ['-1', '1.5', '']) {
      await wrapper.get('[data-test="review-input-tokens"]').setValue(value)
      await wrapper.get('[data-test="review-form"]').trigger('submit')
      expect(api.resolve).not.toHaveBeenCalled()
    }
  })

  it('全零用量不能结算，应在明确无需收费后选择解冻', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    await fillUsage(wrapper)
    for (const field of ['input', 'output', 'cache', 'image']) await wrapper.get(`[data-test="review-${field}-tokens"]`).setValue('0')
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    expect(api.resolve).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('用量不能全部为零；确认无需收费时请取消并选择解冻')
  })

  it('结算只发送人工核实用量，并在等待期间阻止重复提交及关闭', async () => {
    const pending = deferred<{ settled: boolean }>()
    api.resolve.mockReturnValue(pending.promise)
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    await fillUsage(wrapper)
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    await wrapper.get('[data-test="review-cancel"]').trigger('click')
    expect(api.resolve).toHaveBeenCalledTimes(1)
    expect(api.resolve).toHaveBeenCalledWith(7, { action: 'settle', reason: '已核实上游最终账单', usage: { input_tokens: 100, output_tokens: 20, cache_read_input_tokens: 5, image_output_tokens: 6 } })
    expect(wrapper.get('[data-test="review-confirm"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="review-form"]').exists()).toBe(true)
    pending.resolve({ settled: true })
    await flushPromises()
    expect(wrapper.text()).toContain('已按原始定价完成结算')
    expect(wrapper.find('[data-test="review-form"]').exists()).toBe(false)
  })

  it('已保存但未结算时明确提示等待重试，不报结算成功', async () => {
    api.resolve.mockResolvedValue({ settled: false })
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    await fillUsage(wrapper)
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('核对记录已保存，结算等待重试')
    expect(wrapper.text()).not.toContain('已按原始定价完成结算')
  })

  it('已有人工用量等待自动结算时禁用两个人工操作', async () => {
    api.list.mockResolvedValue({ ...page(), items: [{ ...item, review_reason: 'manual_usage_pending', usage: { input_tokens: 100, output_tokens: 20, cache_read_input_tokens: 5, image_output_tokens: 6 } }] })
    const wrapper = await open()
    expect(wrapper.text()).toContain('等待自动结算')
    for (const action of ['settle', 'release']) {
      const button = wrapper.get(`[data-test="review-${action}-7"]`)
      expect(button.attributes('disabled')).toBeDefined()
      await button.trigger('click')
    }
    expect(wrapper.find('[data-test="review-form"]').exists()).toBe(false)
    expect(api.resolve).not.toHaveBeenCalled()
  })

  it('解冻需要明确核实无需收费且不传用量', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-release-7"]').trigger('click')
    expect(wrapper.text()).toContain('已确认无需收费')
    expect(wrapper.find('[data-test="review-input-tokens"]').exists()).toBe(false)
    await wrapper.get('[data-test="review-reason"]').setValue('已核实请求未执行无需收费')
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    await flushPromises()
    expect(api.resolve).toHaveBeenCalledWith(7, { action: 'release', reason: '已核实请求未执行无需收费' })
    expect(wrapper.text()).toContain('冻结金额已释放')
  })

  it('操作失败保留全部输入且可重试，不展示服务端敏感错误', async () => {
    api.resolve.mockRejectedValue({ response: { data: { message: 'upstream-secret-identifier' } } })
    const wrapper = await open()
    await wrapper.get('[data-test="review-settle-7"]').trigger('click')
    await fillUsage(wrapper)
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('操作失败，请稍后重试；核对输入已保留')
    expect((wrapper.get('[data-test="review-reason"]').element as HTMLTextAreaElement).value).toBe('已核实上游最终账单')
    expect((wrapper.get('[data-test="review-input-tokens"]').element as HTMLInputElement).value).toBe('100')
    expect((wrapper.get('[data-test="review-image-tokens"]').element as HTMLInputElement).value).toBe('6')
    expect(wrapper.get('[data-test="review-confirm"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('upstream-secret-identifier')
  })

  it('已存在权威用量等核对冲突不能误报解冻成功', async () => {
    api.resolve.mockRejectedValue({ status: 409, message: 'private backend detail' })
    const wrapper = await open()
    await wrapper.get('[data-test="review-release-7"]').trigger('click')
    await wrapper.get('[data-test="review-reason"]').setValue('已核实请求未执行无需收费')
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('记录已变化或已有计费用量，请重新核实；核对输入已保留')
    expect(wrapper.text()).not.toContain('冻结金额已释放')
    expect(wrapper.text()).not.toContain('private backend detail')
    expect(wrapper.find('[data-test="review-form"]').exists()).toBe(true)
  })

  it.each([
    ['create_result_unknown', '创建结果不确定，需核实是否实际执行'],
    ['manual_usage_pending', '已人工核实用量，等待结算重试'],
    ['upstream-secret-identifier', '请人工核实上游最终状态及实际用量']
  ])('安全解释待核对原因 %s', async (review_reason, message) => {
    api.list.mockResolvedValue({ ...page(), items: [{ ...item, review_reason }] })
    const wrapper = await open()
    expect(wrapper.text()).toContain(message)
    expect(wrapper.text()).not.toContain('upstream-secret-identifier')
  })

  it('核对理由超过500字时拒绝提交，理由两侧空格不计入有效长度', async () => {
    const wrapper = await open()
    await wrapper.get('[data-test="review-release-7"]').trigger('click')
    for (const value of ['理'.repeat(501), '   理由   ']) {
      await wrapper.get('[data-test="review-reason"]').setValue(value)
      await wrapper.get('[data-test="review-form"]').trigger('submit')
      expect(api.resolve).not.toHaveBeenCalled()
      expect(wrapper.text()).toContain('请填写 5–500 字的核对理由')
    }
  })

  it('最后一页完成核对后自动退回有效页', async () => {
    api.list.mockResolvedValueOnce(page(21)).mockResolvedValueOnce(page(21, 2))
      .mockResolvedValueOnce({ ...page(20, 2), items: [] }).mockResolvedValueOnce(page(20))
    const wrapper = await open()
    await wrapper.get('[data-test="review-next"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="review-release-7"]').trigger('click')
    await wrapper.get('[data-test="review-reason"]').setValue('已核实请求未执行无需收费')
    await wrapper.get('[data-test="review-form"]').trigger('submit')
    await flushPromises()
    expect(api.list).toHaveBeenLastCalledWith({ page: 1, page_size: 20 })
    expect(wrapper.text()).toContain('第 1 / 1 页')
  })

  it('加载失败提示可重试且空列表明确说明无需核对', async () => {
    api.list.mockRejectedValueOnce(new Error('private backend error')).mockResolvedValueOnce({ ...page(), items: [], total: 0 })
    const wrapper = await open()
    expect(wrapper.text()).toContain('读取待核对记录失败，请刷新重试')
    expect(wrapper.text()).not.toContain('private backend error')
    await wrapper.get('[data-test="review-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('暂无待核对记录')
  })
})
