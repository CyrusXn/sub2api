import { flushPromises, mount, shallowMount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { UserMonitorView } from '@/api/channelMonitor'
import { status } from '@/api/channelMonitor'
import MonitorCard from '../monitor/MonitorCard.vue'
import MonitorCardGrid from '../monitor/MonitorCardGrid.vue'
import MonitorMetricPair from '../monitor/MonitorMetricPair.vue'
import MonitorDetailDialog from '../MonitorDetailDialog.vue'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))
vi.mock('@/api/channelMonitor', () => ({ status: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorQuotaVisible: () => false }))

function item(id: number, provider: UserMonitorView['provider']): UserMonitorView {
  return { id, name: `group-${id}`, provider, passive: true, group_name: '', primary_model: '',
    primary_status: 'operational', primary_latency_ms: 9999, primary_ping_latency_ms: null,
    success_rate: 0.5, availability_7d: 100, availability_15d: 95, availability_30d: 90, extra_models: [], timeline: [] }
}

describe('真实分组被动卡片', () => {
  it('按固定平台顺序折叠展示，平台内保持使用频率顺序，刷新保留展开状态', async () => {
    const items = [item(8, 'grok'), item(6, 'openai'), item(3, 'anthropic'),
      { ...item(4, 'openai'), name: '【国模】' }, item(1, 'openai'), item(5, 'gemini')]
    const wrapper = shallowMount(MonitorCardGrid, { props: { items, window: '15d', countdownSeconds: 60, loading: false, detailCache: {} } })
    const cards = wrapper.findAllComponents(MonitorCard)
    expect(cards.map(card => card.props('item').id)).toEqual([6, 1, 3, 4, 8, 5])
    expect(cards[0]!.props('availabilityValue')).toBe(95)
    expect(wrapper.findAll('summary').map(summary => summary.findAll('span').map(span => span.text()))).toEqual([
      ['channelStatus.platformGroups.openai', '2'], ['channelStatus.platformGroups.claude', '1'],
      ['channelStatus.platformGroups.cn', '1'], ['channelStatus.platformGroups.grok', '1'],
      ['channelStatus.platformGroups.gemini', '1'],
    ])
    const panels = wrapper.findAll('details')
    expect(panels.map(panel => panel.element.open)).toEqual([true, false, false, false, false])
    panels[0]!.element.open = false
    panels[1]!.element.open = true
    await wrapper.setProps({ items: items.map(group => ({ ...group })) })
    expect(panels[0]!.element.open).toBe(false)
    expect(panels[1]!.element.open).toBe(true)
    wrapper.unmount()
  })

  it('展示真实首字和成功率，详情不会加载旧手工监控接口', async () => {
    const group = item(8, 'openai')
    const card = shallowMount(MonitorCard, { props: { item: group, window: '7d', availabilityValue: 100, countdownSeconds: 60 } })
    const metrics = card.findComponent(MonitorMetricPair)
    expect(metrics.props('primaryLabel')).toBe('channelStatus.passive.firstToken')
    expect(metrics.props('secondaryLabel')).toBe('channelStatus.passive.successRate')
    expect(metrics.props('secondaryValue')).toBe('50.0')
    expect(card.html()).not.toContain('monitorCommon.endpointPing')
    const detail = mount(MonitorDetailDialog, {
      props: { show: true, monitorId: group.id, title: group.name, passiveItem: group },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, MonitorTimeline: true } },
    })
    await flushPromises()
    expect(status).not.toHaveBeenCalled()
    expect(detail.text()).toContain('channelStatus.passive.rules')
    expect(detail.text()).toContain('95.00%')
    card.unmount(); detail.unmount()
  })
})
