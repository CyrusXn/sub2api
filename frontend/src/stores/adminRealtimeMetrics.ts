import { defineStore } from 'pinia'
import { ref } from 'vue'

import { adminAPI } from '@/api/admin'
import type { DashboardRealtimeMetrics } from '@/api/admin/dashboard'

export const useAdminRealtimeMetricsStore = defineStore('adminRealtimeMetrics', () => {
  const metrics = ref<Partial<DashboardRealtimeMetrics> | null>(null)
  const loading = ref(false)
  let activeRequest: Promise<void> | null = null

  // 头部和仪表盘共享同一请求，避免路由切换或连续点击产生重复查询。
  function refresh(): Promise<void> {
    if (activeRequest) return activeRequest

    loading.value = true
    activeRequest = adminAPI.dashboard.getRealtimeMetrics()
      .then(result => {
        metrics.value = { ...metrics.value, ...result }
      })
      .catch(error => {
        console.error('读取管理员实时性能失败:', error)
      })
      .finally(() => {
        loading.value = false
        activeRequest = null
      })

    return activeRequest
  }

  // 兼容旧版实时接口缺少 RPM/TPM 字段，不额外请求完整仪表盘快照。
  function seedDashboardRates(requestsPerMinute: number, tokensPerMinute: number): void {
    metrics.value = {
      ...metrics.value,
      requests_per_minute: metrics.value?.requests_per_minute ?? requestsPerMinute,
      tokens_per_minute: metrics.value?.tokens_per_minute ?? tokensPerMinute
    }
  }

  return {
    metrics,
    loading,
    refresh,
    seedDashboardRates
  }
})
