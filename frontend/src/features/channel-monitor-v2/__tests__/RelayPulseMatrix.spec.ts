const i18nT = (key: string, params?: Record<string, unknown>) => {
  const map: Record<string, string> = {
    'channelMonitorV2.matrix.title': '色块矩阵',
    'channelMonitorV2.matrix.description': '趋势色块视图',
    'channelMonitorV2.matrix.bestTitle': '每分钟最佳请求',
    'channelMonitorV2.matrix.bestDescription': '每分钟选一条真实请求',
    'channelMonitorV2.matrix.bestRequest': '本分钟最佳请求',
    'channelMonitorV2.matrix.minuteWindow': '历史分钟窗口',
    'channelMonitorV2.matrix.outputTpsValue': '输出 Token/秒 {value}',
    'channelMonitorV2.bucket.minutes': '{count}分钟',
    'channelMonitorV2.matrix.wheelZoomX': '滚轮缩放横向',
    'channelMonitorV2.matrix.resetZoom': '重置缩放',
    'channelMonitorV2.matrix.dimension': '维度',
    'channelMonitorV2.metrics.successRate': '成功率',
    'channelMonitorV2.metrics.ttft': '首 Token',
    'channelMonitorV2.metrics.tps': '每秒 Token',
    'channelMonitorV2.metrics.cacheRate': '缓存率',
    'channelMonitorV2.matrix.scoreLine': '评分 {score}',
    'channelMonitorV2.metrics.successRateValue': '成功率 {value}',
    'channelMonitorV2.metrics.errorRateValue': '错误率 {value}',
    'channelMonitorV2.metrics.rpmValue': 'RPM {value}',
    'channelMonitorV2.metrics.tpmValue': 'TPM {value}',
    'channelMonitorV2.metrics.tpsValue': '每秒 Token {value}',
    'channelMonitorV2.metrics.ttftValue': '首 Token {value}',
    'channelMonitorV2.metrics.durationValue': '时长 {value}',
    'channelMonitorV2.metrics.cacheRateValue': '缓存率 {value}',
    'channelMonitorV2.matrix.noTrafficAt': '{time} 无流量',
    'channelMonitorV2.matrix.noTraffic': '无流量',
    'channelMonitorV2.matrix.legendAria': '图例',
    'channelMonitorV2.matrix.bad': '差',
    'channelMonitorV2.matrix.good': '好',
    'channelMonitorV2.matrix.healthyLegend': '健康',
    'channelMonitorV2.matrix.warningLegend': '警告',
    'channelMonitorV2.matrix.criticalLegend': '异常',
    'channelMonitorV2.matrix.unknownLegend': '未知',
    'channelMonitorV2.matrix.unscoredLegend': '有流量 / 暂无评分',
    'channelMonitorV2.matrix.unscoredHint': '该区间有流量；样本不足或当前指标缺失，暂不计算健康分',
  }
  const template = map[key] || key
  return template.replace(/\{(\w+)\}/g, (_, name) => String(params?.[name] ?? ''))
}

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: i18nT,
      te: (key: string) => key.startsWith('channelMonitorV2.'),
      locale: { value: 'zh' },
    }),
  }
})

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import RelayPulseMatrix from '../RelayPulseMatrix.vue'
import type { MonitorHealth, MonitorMetric } from '@/api/channelMonitorV2'

const health: MonitorHealth = {
  overall: 'warning',
  error_rate: 'warning',
  ttft: 'healthy',
  cache: 'warning',
  score: 52,
  error_rate_score: 40,
  ttft_score: 100,
  cache_score: 50,
  minimum_sample: 20,
}

function metrics(requestCount: number): MonitorMetric {
  return {
    success_requests: requestCount ? requestCount - 1 : 0,
    error_requests: requestCount ? 1 : 0,
    request_count: requestCount,
    token_count: 100,
    rpm: 1.5,
    tpm: 10.2,
    output_tps: 22.25,
    error_rate: requestCount ? 1 / requestCount : 0,
    cache_rate: 0.5,
    cache_rate_numerator: 50,
    cache_rate_denominator: 100,
    ttft: { sample_count: requestCount, p50_ms: 100, p95_ms: 300, avg_ms: 150 },
    duration: { sample_count: requestCount, p50_ms: 500, p95_ms: 900, avg_ms: 600 },
    upstream_affected_requests: 2,
    upstream_attempt_count: 3,
  }
}

describe('RelayPulseMatrix', () => {
  it.each([
    ['healthy', 100, 'health-healthy', 10000, '10s'],
    ['warning', 60, 'health-warning', 10001, '10.001s'],
    ['critical', 0, 'health-critical', 1, '1ms'],
  ] as const)('分钟色块只按成功与首字规则显示 %s，不受缓存评分影响', (overall, score, expectedClass, firstToken, display) => {
    const fixedHealth = { ...health, overall, score, cache: 'critical' as const, cache_score: 0, has_traffic: true }
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{ platform: 'openai', metrics: metrics(0), health: fixedHealth, buckets: [
          { bucket_start: '2026-08-01T00:00:00Z', metrics: { ...metrics(0), cache_rate: 0, ttft: { ...metrics(0).ttft, p50_ms: firstToken } }, health: fixedHealth },
        ] }],
        coverage: {
          requested_start: '2026-08-01T00:00:00Z', requested_end: '2026-08-01T00:01:00Z',
          coverage_start: '2026-08-01T00:00:00Z', data_through: '2026-08-01T00:01:00Z',
          computed_at: '2026-08-01T00:01:00Z', aggregation_lag_seconds: 0, coverage_complete: true, bucket_seconds: 60,
        },
        showThroughput: false,
      },
    })
    expect(wrapper.get('.pulse-cell').classes()).toContain(expectedClass)
    expect(wrapper.get('.pulse-cell').attributes('title')).toContain(`评分 ${score}`)
    expect(wrapper.get('.pulse-cell').attributes('title')).toContain(`首 Token ${display}`)
    wrapper.unmount()
  })

  it('distinguishes unscored traffic from no traffic after public volume redaction', async () => {
    const unknownHealth = {
      overall: 'unknown' as const, error_rate: 'unknown' as const,
      ttft: 'unknown' as const, cache: 'unknown' as const,
      minimum_sample: 50, has_traffic: true,
    }
    const redacted = { ...metrics(0), rpm: 0, tpm: 0 }
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{
          platform: 'openai', group_id: 7, metrics: redacted, health: unknownHealth,
          buckets: [
            { bucket_start: '2026-08-01T00:00:00Z', metrics: redacted, health: unknownHealth },
            { bucket_start: '2026-08-01T00:01:00Z', metrics: redacted, health: { ...unknownHealth, has_traffic: false } },
          ],
        }],
        coverage: {
          requested_start: '2026-08-01T00:00:00Z', requested_end: '2026-08-01T00:03:00Z',
          coverage_start: '2026-08-01T00:00:00Z', data_through: '2026-08-01T00:03:00Z',
          computed_at: '2026-08-01T00:03:00Z', aggregation_lag_seconds: 0,
          coverage_complete: true, bucket_seconds: 60,
        },
        showThroughput: false,
      },
    })
    const cells = wrapper.findAll('.pulse-cell')
    expect(cells[0].classes()).toContain('health-unscored')
    expect(cells[0].attributes('title')).toContain('样本不足或当前指标缺失')
    await cells[0].trigger('mouseenter')
    expect(document.querySelector('.matrix-floating-tooltip')?.textContent).toContain('该区间有流量')
    expect(cells[1].classes()).toContain('health-unknown')
    expect(cells[1].attributes('title')).toContain('无流量')
    expect(cells[1].attributes('title')).not.toContain('100.0%')
    expect(cells[2].classes()).toContain('health-unknown')
    expect(wrapper.get('.dimension-cell .status-dot').classes()).toContain('health-unscored')
    const legacyHealth = { ...unknownHealth, has_traffic: undefined }
    await wrapper.setProps({
      rows: [{
        platform: 'openai', group_id: 7, metrics: metrics(0), health: legacyHealth,
        buckets: [{ bucket_start: '2026-08-01T00:00:00Z', metrics: metrics(0), health: legacyHealth }],
      }],
    })
    expect(wrapper.findAll('.pulse-cell')[0].classes()).toContain('health-unscored')
    wrapper.unmount()
  })

  it('shows privacy-safe hover tooltips and three-state colors without click modal', async () => {
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{
          platform: 'openai',
          group_id: 7,
          group_name: '默认组',
          model: 'gpt-5',
          metrics: metrics(10),
          health,
          buckets: [
            { bucket_start: '2026-08-01T00:00:00Z', metrics: metrics(10), health },
            { bucket_start: '2026-08-01T00:01:00Z', metrics: metrics(0), health },
          ],
        }],
        coverage: {
          requested_start: '2026-08-01T00:00:00Z',
          requested_end: '2026-08-01T00:03:00Z',
          coverage_start: '2026-08-01T00:00:00Z',
          data_through: '2026-08-01T00:03:00Z',
          computed_at: '2026-08-01T00:03:00Z',
          aggregation_lag_seconds: 0,
          coverage_complete: true,
          bucket_seconds: 60,
        },
      },
    })

    const cells = wrapper.findAll('.pulse-cell')
    expect(wrapper.findAll('.matrix-header span').find(span => span.text() === '缓存率')?.attributes('title'))
      .toBe('channelMonitorV2.metrics.cacheRateHint')
    expect(cells).toHaveLength(3)
    // Hover tooltip content (privacy-safe: no absolute request/error counts)
    const tip = cells[0].text()
    expect(tip).toContain('成功率')
    expect(tip).toContain('90.0%')
    expect(tip).toContain('首 Token')
    expect(tip).toContain('输出 Token/秒 22.25')
    expect(tip).toContain('缓存率')
    expect(tip).toContain('本分钟最佳请求')
    expect(tip).not.toContain('RPM')
    expect(tip).not.toContain('请求数')
    expect(tip).not.toContain('用户错误')
    expect(tip).not.toContain('上游受影响')
    // Summary columns: success · ttft · tokens/s · cache
    const header = wrapper.find('.matrix-header').text()
    expect(header).toContain('成功率')
    expect(header).toContain('首 Token')
    expect(header).toContain('每秒 Token')
    expect(header).toContain('缓存率')
    expect(cells[0].classes()).toContain('health-warning')
    // Redacted user payloads may have request_count=0 but still include score.
    expect(cells[1].classes()).toContain('health-warning')
    expect(cells[2].classes()).toContain('health-unknown')

    await wrapper.setProps({
      minuteWindow: { range_start: '2026-07-31T00:00:00Z', range_end: '2026-08-01T00:03:00Z', offset: 0, max_offset: 1083 },
    })
    await wrapper.get('input[type="range"]').setValue('723')
    expect(wrapper.emitted('minuteOffsetChange')).toEqual([[360]])

    // No click-to-open modal
    await cells[0].trigger('click')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)

    const resetButton = wrapper.findAll('button').find(button => button.text() === '重置缩放')!
    const originalAxis = wrapper.get('.pulse-axis').text()
    for (const selector of ['.dimension-cell strong', '.summary-value', '.matrix-header', '.matrix-scroll']) {
      const event = new WheelEvent('wheel', { deltaY: -100, bubbles: true, cancelable: true })
      wrapper.get(selector).element.dispatchEvent(event)
      await wrapper.vm.$nextTick()
      expect(event.defaultPrevented).toBe(false)
      expect(resetButton.attributes('disabled')).toBeDefined()
      expect(wrapper.get('.pulse-axis').text()).toBe(originalAxis)
    }
    const zoomEvent = new WheelEvent('wheel', { deltaY: -100, bubbles: true, cancelable: true })
    cells[0].element.dispatchEvent(zoomEvent)
    await wrapper.vm.$nextTick()
    expect(zoomEvent.defaultPrevented).toBe(true)
    expect(resetButton.attributes('disabled')).toBeUndefined()
  })
})

describe('RelayPulseMatrix axis range', () => {
  it('uses selected requested range for the X axis even with partial coverage', () => {
    const wrapper = mount(RelayPulseMatrix, {
      props: {
        rows: [{
          platform: 'openai',
          group_id: 7,
          group_name: '默认组',
          model: 'gpt-5',
          metrics: metrics(10),
          health,
          buckets: [
            { bucket_start: '2026-08-01T00:02:00Z', metrics: metrics(10), health },
          ],
        }],
        coverage: {
          requested_start: '2026-08-01T00:00:00Z',
          requested_end: '2026-08-01T00:05:00Z',
          coverage_start: '2026-08-01T00:02:00Z',
          data_through: '2026-08-01T00:03:00Z',
          computed_at: '2026-08-01T00:03:00Z',
          aggregation_lag_seconds: 0,
          coverage_complete: false,
          bucket_seconds: 60,
        },
      },
    })
    // 5 minutes @ 60s buckets → 5 cells spanning the selected range
    expect(wrapper.findAll('.pulse-cell')).toHaveLength(5)
  })
})
