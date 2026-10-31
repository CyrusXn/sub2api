import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAutoRefresh } from '../useAutoRefresh'

describe('useAutoRefresh', () => {
  let wrapper: ReturnType<typeof mount> | undefined

  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    vi.useRealTimers()
    localStorage.clear()
  })

  it('refreshes when the countdown reaches zero', async () => {
    const onRefresh = vi.fn().mockResolvedValue(undefined)
    let refresh: ReturnType<typeof useAutoRefresh>
    wrapper = mount(defineComponent({
      setup() {
        refresh = useAutoRefresh({ storageKey: 'test-refresh', intervals: [2], defaultEnabled: true, onRefresh })
        return () => h('span', String(refresh.countdown.value))
      },
    }))

    refresh!.start()
    await vi.advanceTimersByTimeAsync(1999)
    expect(onRefresh).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  it('runs a refresh missed while paused as soon as the pause clears', async () => {
    const onRefresh = vi.fn().mockResolvedValue(undefined)
    let paused = true
    let refresh: ReturnType<typeof useAutoRefresh>
    wrapper = mount(defineComponent({
      setup() {
        refresh = useAutoRefresh({
          storageKey: 'test-refresh', intervals: [1], defaultEnabled: true, onRefresh,
          shouldPause: () => paused,
        })
        return () => h('span', String(refresh.countdown.value))
      },
    }))

    refresh!.start()
    await vi.advanceTimersByTimeAsync(1000)
    expect(onRefresh).not.toHaveBeenCalled()
    paused = false
    await vi.advanceTimersByTimeAsync(1000)
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  it('回到前台立即补一次过期刷新，不按后台遗漏次数重复请求', async () => {
    const onRefresh = vi.fn().mockResolvedValue(undefined)
    let refresh: ReturnType<typeof useAutoRefresh>
    wrapper = mount(defineComponent({
      setup() {
        refresh = useAutoRefresh({ storageKey: 'test-refresh', intervals: [5], onRefresh })
        return () => h('span')
      },
    }))
    refresh!.setEnabled(true)
    vi.setSystemTime(Date.now() + 60_000)
    document.dispatchEvent(new Event('visibilitychange'))
    await Promise.resolve()
    expect(onRefresh).toHaveBeenCalledTimes(1)
    document.dispatchEvent(new Event('visibilitychange'))
    await Promise.resolve()
    expect(onRefresh).toHaveBeenCalledTimes(1)
    expect(refresh!.countdown.value).toBe(5)
  })

  it('慢请求不重入，完成后重新倒计时', async () => {
    let resolve!: () => void
    const onRefresh = vi.fn(() => new Promise<void>(done => { resolve = done }))
    let refresh: ReturnType<typeof useAutoRefresh>
    wrapper = mount(defineComponent({
      setup() {
        refresh = useAutoRefresh({ storageKey: 'test-refresh', intervals: [2], onRefresh })
        return () => h('span')
      },
    }))
    refresh!.setEnabled(true)
    await vi.advanceTimersByTimeAsync(6000)
    expect(onRefresh).toHaveBeenCalledTimes(1)
    expect(refresh!.countdown.value).toBe(0)
    resolve()
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(1999)
    expect(onRefresh).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(onRefresh).toHaveBeenCalledTimes(2)
    refresh!.setEnabled(false)
    resolve()
    await Promise.resolve()
    expect(refresh!.countdown.value).toBe(0)
  })

  it('失败后释放刷新锁并按下个周期重试，卸载后停止', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const onRefresh = vi.fn().mockRejectedValueOnce(new Error('测试失败')).mockResolvedValue(undefined)
    let refresh: ReturnType<typeof useAutoRefresh>
    wrapper = mount(defineComponent({
      setup() {
        refresh = useAutoRefresh({ storageKey: 'test-refresh', intervals: [2], onRefresh })
        return () => h('span')
      },
    }))
    refresh!.setEnabled(true)
    await vi.advanceTimersByTimeAsync(4000)
    expect(onRefresh).toHaveBeenCalledTimes(2)
    expect(refresh!.fetching.value).toBe(false)
    wrapper.unmount()
    wrapper = undefined
    refresh!.start()
    vi.setSystemTime(Date.now() + 60_000)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(4000)
    expect(onRefresh).toHaveBeenCalledTimes(2)
    error.mockRestore()
  })
})
