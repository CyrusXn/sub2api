import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BalanceCenterView from '../BalanceCenterView.vue'

const api = vi.hoisted(() => ({
  sites: vi.fn(), rechargeSummary: vi.fn(), createRechargeEvent: vi.fn(), deleteRechargeEvent: vi.fn()
}))
const notifications = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { balanceCenter: api } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key })
}))

const sites = [
  { id: 1, name: 'HBY', normalized_domain: 'hby.example', base_url: 'https://hby.example', source: 'sub2api', probe_supported: true, updated_at: '2026-08-14T00:00:00Z', historical_recharge_total: 20 },
  { id: 2, name: 'VoVo', normalized_domain: 'vovo.example', base_url: 'https://vovo.example', source: 'sub2api', probe_supported: true, updated_at: '2026-08-14T00:00:00Z', historical_recharge_total: 80 }
]
const summary = {
  total_amount: 80,
  total: 2,
  page: 1,
  page_size: 20,
  items: [
    { id: 12, source: 'manual', source_key: 'tx-12', site_id: 1, amount: 50, currency: 'CNY', occurred_at: '2026-08-14T02:10:00Z', note: '' },
    { id: 11, source: 'manual', source_key: 'tx-11', site_id: 1, amount: 30, currency: 'CNY', occurred_at: '2026-08-13T02:10:00Z', note: '' }
  ],
  sites: [{ site_id: 1, site_name: 'HBY', total_amount: 80, record_count: 2, items: [
    { id: 12, source: 'manual', source_key: 'tx-12', site_id: 1, amount: 50, currency: 'CNY', occurred_at: '2026-08-14T02:10:00Z', note: '' },
    { id: 11, source: 'manual', source_key: 'tx-11', site_id: 1, amount: 30, currency: 'CNY', occurred_at: '2026-08-13T02:10:00Z', note: '' }
  ] }]
}

const mountView = () => mount(BalanceCenterView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      Pagination: true,
      Icon: true,
      RouterLink: { props: ['to'], template: '<a><slot /></a>' }
    }
  }
})

describe('BalanceCenterView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal('confirm', vi.fn(() => true))
    api.sites.mockResolvedValue(sites)
    api.rechargeSummary.mockResolvedValue(summary)
    api.createRechargeEvent.mockResolvedValue(summary.items[0])
    api.deleteRechargeEvent.mockResolvedValue(undefined)
  })

  it('默认只展示新增充值并仅加载站点', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(api.sites).toHaveBeenCalledOnce()
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="tab-add-recharge"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.findAll('[data-test="site-recharge-row"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="total-amount"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="recharge-list-scroll"]').classes()).toContain('overflow-y-auto')
    expect(wrapper.findAll('[data-test="site-recharge-row"]')[0].text()).toContain('VoVo')
  })

  it('回到此刻按钮将充值时间重置为当前秒', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="recharge-time-now"]').trigger('click')
    expect((wrapper.get('[data-test="recharge-time"]').element as HTMLInputElement).value).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.000)?$/)
  })

  it('切换到充值记录时才加载汇总并隐藏新增充值', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()

    expect(api.rechargeSummary).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="tab-recharge-records"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-test="total-amount"]').text()).toContain('80.00')
    expect(wrapper.find('[data-test="site-recharge-row"]').exists()).toBe(false)
  })

  it('站点金额回车后使用当前秒新增并刷新统计', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="recharge-time"]').setValue('2026-08-14T10:20')
    const input = wrapper.get('[data-test="recharge-amount-1"]')
    await input.setValue('88.5')
    await input.trigger('keyup.enter')
    await flushPromises()

    expect(api.createRechargeEvent).toHaveBeenCalledWith(expect.objectContaining({
      source: 'manual', site_id: 1, amount: 88.5, currency: 'CNY', occurred_at: '2026-08-14T02:20:00.000Z'
    }))
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    expect((input.element as HTMLInputElement).value).toBe('')
  })

  it('选择快捷时间后直接刷新且无需确定按钮', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="range-yesterday"]').trigger('click')
    await flushPromises()

    expect(api.rechargeSummary).toHaveBeenCalledTimes(2)
    const params = api.rechargeSummary.mock.calls[1][0]
    expect(params.start_time).toBeTruthy()
    expect(params.end_time).toBeTruthy()
    expect(wrapper.find('[data-test="apply-range"]').exists()).toBe(false)
  })

  it('时间维度删除后重查总额，站点维度可展开多个时间节点', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="delete-recharge-12"]').trigger('click')
    await flushPromises()

    expect(api.deleteRechargeEvent).toHaveBeenCalledWith(12)
    expect(api.rechargeSummary).toHaveBeenCalledTimes(2)

    await wrapper.get('[data-test="dimension-site"]').trigger('click')
    await wrapper.get('[data-test="expand-site-1"]').trigger('click')
    expect(wrapper.findAll('[data-test="site-history-item"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('HBY')
  })

  it('站点维度按筛选范围内充值总额降序展示', async () => {
    api.rechargeSummary.mockResolvedValue({
      ...summary,
      sites: [
        { site_id: 1, site_name: 'HBY', total_amount: 20, record_count: 1, items: [] },
        { site_id: 2, site_name: 'VoVo', total_amount: 80, record_count: 1, items: [] }
      ]
    })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="dimension-site"]').trigger('click')
    expect(wrapper.findAll('[data-test="site-summary-row"]')[0].text()).toContain('VoVo')
  })

  it('未绑定账号的旧站点仍按独立标签展示', async () => {
    api.rechargeSummary.mockResolvedValue({
      ...summary,
      total_amount: 10,
      total: 1,
      items: [{ id: 21, source: 'legacy_opening', source_key: 'opening:fox', site_label: 'Fox', amount: 10, currency: 'CNY', occurred_at: '2026-08-11T00:00:00Z', note: '旧系统期初累计，历史日期未知' }],
      sites: [{ site_name: 'Fox', total_amount: 10, record_count: 1, items: [] }]
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Fox')
    expect(wrapper.text()).toContain('admin.balanceCenter.unknownRechargeDate')
    expect(wrapper.text()).not.toContain('2026-08-11')
    expect(wrapper.text()).not.toContain('admin.balanceCenter.unassignedSite')
  })
})
