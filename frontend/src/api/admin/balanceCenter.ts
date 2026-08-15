import { apiClient } from '../client'

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
  normalized_domain: string
  base_url: string
  source: string
  probe_supported: boolean
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

export const balanceCenterAPI = {
  async overview() {
    return (await apiClient.get<BalanceCenterOverviewItem[]>(`${base}/overview`)).data
  },
  async sites() {
    return (await apiClient.get<BalanceCenterSite[]>(`${base}/sites`)).data
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
