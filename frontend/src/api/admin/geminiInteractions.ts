import { apiClient } from '../client'

export interface GeminiInteractionUsage {
  input_tokens: number
  output_tokens: number
  cache_read_input_tokens: number
  image_output_tokens?: number
}

export interface GeminiInteractionReviewItem {
  id: number
  public_id: string
  user_id: number
  api_key_id: number
  group_id: number | null
  account_id: number | null
  model: string
  status: string
  hold_amount: number
  review_reason: string
  usage?: GeminiInteractionUsage | null
  created_at: string
}

export interface GeminiInteractionReviewPage {
  items: GeminiInteractionReviewItem[]
  total: number
  page: number
  page_size: number
}

export type GeminiInteractionResolution = {
  action: 'settle'
  reason: string
  usage: GeminiInteractionUsage & { image_output_tokens: number }
} | {
  action: 'release'
  reason: string
}

const base = '/admin/balance-center/interactions'

export const geminiInteractionsAPI = {
  async list(params: { page: number; page_size: number }) {
    return (await apiClient.get<GeminiInteractionReviewPage>(base, { params })).data
  },
  async resolve(id: number, payload: GeminiInteractionResolution) {
    return (await apiClient.post<{ settled: boolean }>(`${base}/${id}/resolve`, payload)).data
  }
}
