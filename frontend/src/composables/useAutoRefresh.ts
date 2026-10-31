import { ref, onBeforeUnmount, type Ref } from 'vue'

export interface UseAutoRefreshOptions {
  storageKey?: string
  intervals?: readonly number[]
  defaultInterval?: number
  defaultEnabled?: boolean
  onRefresh: () => Promise<void> | void
  /** 仅暂缓请求，不冻结倒计时；页面正在加载时避免重复请求。 */
  shouldPause?: () => boolean
}

export function useAutoRefresh(options: UseAutoRefreshOptions) {
  const {
    storageKey,
    intervals = [5, 10, 15, 30] as const,
    defaultInterval,
    onRefresh,
    shouldPause,
  } = options

  const enabled = ref(options.defaultEnabled ?? false)
  const intervalSeconds = ref(defaultInterval ?? intervals[intervals.length - 1])
  const countdown = ref(0)
  const fetching = ref(false)

  let timerId: number | undefined
  let nextRefreshAt = 0
  let disposed = false

  function loadFromStorage() {
    try {
      if (!storageKey) return
      const saved = localStorage.getItem(storageKey)
      if (!saved) return
      const parsed = JSON.parse(saved) as { enabled?: boolean; interval_seconds?: number }
      enabled.value = parsed.enabled === true
      const iv = Number(parsed.interval_seconds)
      if (intervals.includes(iv as any)) intervalSeconds.value = iv
    } catch { /* ignore */ }
  }

  function saveToStorage() {
    try {
      if (!storageKey) return
      localStorage.setItem(storageKey, JSON.stringify({
        enabled: enabled.value,
        interval_seconds: intervalSeconds.value,
      }))
    } catch { /* ignore */ }
  }

  async function tick() {
    if (!enabled.value || fetching.value || timerId === undefined) return
    // 按实际截止时间计时，后台标签页节流后也能立即补刷。
    countdown.value = Math.max(0, Math.ceil((nextRefreshAt - Date.now()) / 1000))
    if (countdown.value > 0 || shouldPause?.()) return
    fetching.value = true
    try {
      await onRefresh()
    } catch (error) {
      console.error('自动刷新页面数据失败：', error)
    } finally {
      fetching.value = false
      if (enabled.value && !disposed) resetCountdown()
    }
  }

  function handleVisibilityChange() {
    if (!document.hidden) void tick()
  }

  function start() {
    if (disposed || timerId !== undefined) return
    resetCountdown()
    timerId = setInterval(() => { void tick() }, 1000) as unknown as number
    document.addEventListener('visibilitychange', handleVisibilityChange)
  }

  function stop() {
    if (timerId !== undefined) {
      clearInterval(timerId)
      timerId = undefined
    }
    document.removeEventListener('visibilitychange', handleVisibilityChange)
  }

  function setEnabled(value: boolean) {
    enabled.value = value
    saveToStorage()
    if (value) {
      resetCountdown()
      start()
    } else {
      stop()
      countdown.value = 0
    }
  }

  function setInterval_(seconds: number) {
    if (!Number.isFinite(seconds) || seconds <= 0) return
    intervalSeconds.value = seconds
    saveToStorage()
    if (enabled.value) resetCountdown()
  }

  function resetCountdown() {
    nextRefreshAt = Date.now() + intervalSeconds.value * 1000
    countdown.value = intervalSeconds.value
  }

  loadFromStorage()

  onBeforeUnmount(() => {
    disposed = true
    stop()
  })

  return {
    enabled: enabled as Ref<boolean>,
    intervalSeconds: intervalSeconds as Ref<number>,
    countdown: countdown as Ref<number>,
    fetching: fetching as Ref<boolean>,
    intervals,
    setEnabled,
    setInterval: setInterval_,
    resetCountdown,
    start,
    stop,
  }
}
