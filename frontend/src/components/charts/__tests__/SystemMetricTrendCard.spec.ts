import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import SystemMetricTrendCard from '../SystemMetricTrendCard.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('vue-chartjs', () => ({
  Line: {
    name: 'Line',
    props: ['data', 'options'],
    template: '<div data-testid="line-chart" />'
  }
}))

const metricPoint = {
  time: '2026-08-17T08:00:00Z',
  cpu_usage_percent: 6.5,
  memory_used_mb: 100,
  memory_total_mb: 1000,
  memory_usage_percent: 10,
  network_receive_bytes_per_second: 1_500_000,
  network_transmit_bytes_per_second: 2_500_000,
  disk_used_bytes: 10,
  disk_total_bytes: 100,
  disk_usage_percent: 10,
  resource_source: 'host'
}

const mountCard = (metric: 'cpu' | 'memory' | 'network' | 'disk', extraProps = {}) => mount(SystemMetricTrendCard, {
  props: {
    title: '趋势',
    metric,
    points: [metricPoint],
    loading: false,
    ...extraProps
  } as any,
  global: {
    stubs: {
      Icon: true,
      LoadingSpinner: true
    }
  }
})

describe('SystemMetricTrendCard', () => {
  it('shows explicit percent units on resource chart axes and tooltips', () => {
    const wrapper = mountCard('cpu')
    const chart = wrapper.findComponent({ name: 'Line' })
    const options = chart.props('options') as any

    expect(options.scales.y.title).toMatchObject({ display: true, text: '%' })
    expect(options.plugins.tooltip.callbacks.label({ dataset: { label: 'CPU' }, raw: 6.5 })).toContain('6.5%')
  })

  it('uses Mbps for realtime bandwidth rates', () => {
    const wrapper = mountCard('network')
    const chart = wrapper.findComponent({ name: 'Line' })
    const options = chart.props('options') as any

    expect(options.scales.y.title).toMatchObject({ display: true, text: 'Mbps' })
    expect((chart.props('data') as any).datasets[0].data).toEqual([12])
    expect((chart.props('data') as any).datasets[1].data).toEqual([20])
    expect(options.plugins.tooltip.callbacks.label({ dataset: { label: 'admin.dashboard.networkReceive' }, raw: 12 })).toContain('12.00 Mbps')
  })

  it('switches bandwidth from realtime rate to daily usage and shows range totals', async () => {
    const wrapper = mountCard('network', {
      networkDaily: [
        { bucket_date: '2026-08-16', receive_bytes: 3_000_000_000, transmit_bytes: 2_000_000_000, total_bytes: 5_000_000_000 },
        { bucket_date: '2026-08-17', receive_bytes: 4_000_000_000, transmit_bytes: 1_000_000_000, total_bytes: 5_000_000_000 }
      ],
      networkTotals: { receive_bytes: 7_000_000_000, transmit_bytes: 3_000_000_000, total_bytes: 10_000_000_000 }
    })

    expect(wrapper.text()).toContain('admin.dashboard.networkRate')
    expect(wrapper.text()).toContain('admin.dashboard.networkDailyUsage')

    await wrapper.get('[data-testid="network-mode-daily"]').trigger('click')

    expect(wrapper.text()).toContain('admin.dashboard.rangeTotalTraffic')
    expect(wrapper.text()).toContain('10.00 GB')
    const chart = wrapper.findComponent({ name: 'Line' })
    expect((chart.props('options') as any).scales.y.title.text).toBe('GB')
    expect((chart.props('data') as any).datasets[0].data).toEqual([3, 4])
  })
})
