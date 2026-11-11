import { apiClient, buildGatewayUrl } from '../client'

export interface UpstreamSiteBalanceReminder {
  host: string
  display_name: string
  old_balance: number
  new_balance: number
  delta: number
  currency: string
  detected_at: string
  version: number
}

export interface UpstreamSiteBalanceReminderEvent {
  type: 'snapshot' | 'upsert' | 'resolved'
  reminders?: UpstreamSiteBalanceReminder[]
  versions?: Record<string, number>
  reminder?: UpstreamSiteBalanceReminder
  host?: string
  version?: number
}

export interface SubscribeUpstreamSiteBalanceRemindersOptions {
  token?: string | null
  onStatusChange?: (status: 'connecting' | 'connected' | 'reconnecting' | 'offline') => void
  reconnectBaseDelayMs?: number
  reconnectMaxDelayMs?: number
  wsBaseUrl?: string
}

export function subscribeUpstreamSiteBalanceReminders(
  onEvent: (event: UpstreamSiteBalanceReminderEvent) => void,
  options: SubscribeUpstreamSiteBalanceRemindersOptions = {}
): () => void {
  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectAttempts = 0
  let stopped = false
  const baseDelay = options.reconnectBaseDelayMs ?? 1000
  const maxDelay = options.reconnectMaxDelayMs ?? 30000

  const connect = () => {
    if (stopped) return
    options.onStatusChange?.(reconnectAttempts === 0 ? 'connecting' : 'reconnecting')
    const wsBaseUrl = options.wsBaseUrl || import.meta.env.VITE_WS_BASE_URL
    const url = wsBaseUrl
      ? new URL(`${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${wsBaseUrl}/api/v1/admin/balance-center/upstream-site-reminders/ws`)
      : new URL(buildGatewayUrl('/api/v1/admin/balance-center/upstream-site-reminders/ws').replace(/^http/, 'ws'))
    const token = String(options.token ?? localStorage.getItem('auth_token') ?? '').trim()
    const protocols = ['sub2api-admin']
    if (token) protocols.push(`jwt.${token}`)
    socket = new WebSocket(url.toString(), protocols)
    socket.onopen = () => {
      reconnectAttempts = 0
      options.onStatusChange?.('connected')
    }
    socket.onmessage = (message) => {
      try {
        const event = JSON.parse(String(message.data)) as UpstreamSiteBalanceReminderEvent
        if (event.type === 'snapshot' || event.type === 'resolved' || (event.type === 'upsert' && event.reminder)) {
          onEvent(event)
        }
      } catch {
        // 忽略格式异常的通知帧；活动状态会在下次连接时从服务端快照恢复。
      }
    }
    socket.onclose = () => {
      socket = null
      if (stopped) return
      if (typeof navigator !== 'undefined' && 'onLine' in navigator && !navigator.onLine) {
        options.onStatusChange?.('offline')
        return
      }
      const delay = Math.min(baseDelay * 2 ** Math.min(reconnectAttempts, 8), maxDelay)
      reconnectAttempts += 1
      reconnectTimer = setTimeout(connect, delay + Math.floor(Math.random() * 250))
    }
    socket.onerror = () => socket?.close()
  }

  const handleOnline = () => {
    if (stopped || (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING))) return
    if (reconnectTimer) clearTimeout(reconnectTimer)
    reconnectTimer = null
    connect()
  }

  window.addEventListener('online', handleOnline)
  connect()
  return () => {
    stopped = true
    window.removeEventListener('online', handleOnline)
    if (reconnectTimer) clearTimeout(reconnectTimer)
    socket?.close()
    socket = null
  }
}

export interface BalanceCenterSettings {
  enabled: boolean
  event_probe_enabled: boolean
  email_enabled: boolean
  low_balance_threshold: number
}

export interface BalanceCenterOverviewItem {
  site_id: number
  site_name: string
  normalized_domain: string
  base_url: string
  account_id?: number
  account_name: string
  status: string
  balance?: number
  converted_balance?: number
  rate_multiplier?: number
  conversion_scale: number
  currency: string
  reason: string
  probed_at: string
  last_used_at?: string
  source: string
}

export interface BalanceCenterSite {
  id: number
  name: string
  display_name: string
  normalized_domain: string
  base_url: string
  source: string
  probe_supported: boolean
  historical_recharge_total: number
  updated_at: string
}

export interface BalanceCenterSnapshot extends BalanceCenterOverviewItem {
  id: number
  source_key: string
}

export interface BalanceCenterManualRow {
  id?: number
  source: string
  source_key: string
  site_id?: number
  label: string
  expression: string
  amount: number
  sort_order: number
}

export interface BalanceCenterRechargeEvent {
  id?: number
  source: string
  source_key: string
  site_id?: number
  site_label?: string
  account_id?: number
  amount: number
  currency: string
  occurred_at: string
  note: string
  record_type: 'recharge' | 'subscription'
}

export interface BalanceCenterRechargeSiteSummary {
  site_id?: number
  site_name: string
  total_amount: number
  record_count: number
  items: BalanceCenterRechargeEvent[]
}

export interface BalanceCenterRechargeSummary extends BalanceCenterPage<BalanceCenterRechargeEvent> {
  total_amount: number
  sites: BalanceCenterRechargeSiteSummary[]
}

export interface BalanceCenterReconciliation {
  id?: number
  source: string
  source_key: string
  period_start?: string
  period_end?: string
  expected_amount?: number
  actual_amount?: number
  difference_amount?: number
  status: string
  created_at?: string
}

export interface BalanceCenterAlert {
  id: number
  site_id: number
  account_id?: number
  alert_type: string
  recipient_email: string
  old_value?: number
  new_value?: number
  threshold?: number
  status: string
  failure_reason: string
  attempted_at?: string
  accepted_at?: string
  created_at: string
}

export interface BalanceCenterPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface BalanceCenterListParams {
  page?: number
  page_size?: number
  site_id?: number
  account_id?: number
  status?: string
  start_time?: string
  end_time?: string
}

const base = '/admin/balance-center'

// 订阅是已入账充值的剩余资产，保存配置不会追加充值。
export interface BalanceCenterAsset {
  site_id: number
  site_name: string
  domain: string
  account_id: number | null
  manual_balance: number | null
  cash_balance: number | null
  subscription_id: number | null
  subscription_price: number
  subscription_days: number
  subscription_expires_at: string | null
  subscription_auto_sync: boolean
  subscription_synced_at: string | null
  subscription_sync_error: string
  subscription_daily_remaining_usd?: number | null
  subscription_balance: number | null
  total_balance: number | null
  balance_known: boolean
}

export const balanceCenterAPI = {
  async setNextLowBalanceThreshold(accountId: number, threshold: number) {
    return (await apiClient.post(`${base}/alerts/next-threshold`, {
      account_id: accountId,
      threshold,
    })).data
  },
  async assets() {
    return (await apiClient.get<BalanceCenterAsset[]>(`${base}/assets`)).data
  },
  async saveAsset(asset: BalanceCenterAsset) {
    await apiClient.put(`${base}/assets/${asset.site_id}`, asset)
  },
  async syncSubscription(siteID: number) {
    return (await apiClient.post<BalanceCenterAsset>(`${base}/assets/${siteID}/sync`)).data
  },
  async overview() {
    return (await apiClient.get<BalanceCenterOverviewItem[]>(`${base}/overview`)).data
  },
  async sites() {
    return (await apiClient.get<BalanceCenterSite[]>(`${base}/sites`)).data
  },
  async createSite(item: { name: string; base_url: string }) {
    return (await apiClient.post<BalanceCenterSite>(`${base}/sites`, item)).data
  },
  async renameSite(id: number, name: string) {
    await apiClient.patch(`${base}/sites/${id}`, { name })
  },
  async snapshots(params: BalanceCenterListParams) {
    return (await apiClient.get<BalanceCenterPage<BalanceCenterSnapshot>>(`${base}/snapshots`, { params })).data
  },
  async getSettings() {
    return (await apiClient.get<BalanceCenterSettings>(`${base}/settings`)).data
  },
  async updateSettings(settings: BalanceCenterSettings) {
    return (await apiClient.put<BalanceCenterSettings>(`${base}/settings`, settings)).data
  },
  async probeAccounts(accountIds: number[]) {
    return (await apiClient.post(`${base}/accounts/probe`, { account_ids: accountIds })).data
  },
  async manualRows() {
    return (await apiClient.get<BalanceCenterManualRow[]>(`${base}/manual-rows`)).data
  },
  async replaceManualRows(items: BalanceCenterManualRow[]) {
    return (await apiClient.put(`${base}/manual-rows`, { items })).data
  },
  async rechargeEvents(params: BalanceCenterListParams) {
    return (await apiClient.get<BalanceCenterPage<BalanceCenterRechargeEvent>>(`${base}/recharge-events`, { params })).data
  },
  async rechargeSummary(params: BalanceCenterListParams) {
    return (await apiClient.get<BalanceCenterRechargeSummary>(`${base}/recharge-summary`, { params })).data
  },
  async createRechargeEvent(item: BalanceCenterRechargeEvent) {
    return (await apiClient.post<BalanceCenterRechargeEvent>(`${base}/recharge-events`, item)).data
  },
  async deleteRechargeEvent(id: number) {
    await apiClient.delete(`${base}/recharge-events/${id}`)
  },
  async reconciliations(params: BalanceCenterListParams) {
    return (await apiClient.get<BalanceCenterPage<BalanceCenterReconciliation>>(`${base}/reconciliations`, { params })).data
  },
  async createReconciliation(item: BalanceCenterReconciliation) {
    return (await apiClient.post<BalanceCenterReconciliation>(`${base}/reconciliations`, item)).data
  },
  async alerts(params: BalanceCenterListParams) {
    return (await apiClient.get<BalanceCenterPage<BalanceCenterAlert>>(`${base}/alerts`, { params })).data
  },
  async syncAutomaticRecords() {
    return (await apiClient.post(`${base}/automatic-records/sync`)).data
  },
  async saveLiandongSession(curl: string) {
    return (await apiClient.post(`${base}/liandong/session`, { curl })).data
  },
  async syncLiandong() {
    return (await apiClient.post(`${base}/liandong/sync`)).data
  }
}

export default balanceCenterAPI
