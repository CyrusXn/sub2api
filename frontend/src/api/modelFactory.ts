import apiClient from './client'

export interface ModelFactoryGroup {
  id: number
  name: string
  platform: string
  rate_multiplier: number
  models: string[]
}

export interface ModelFactoryResponse { groups: ModelFactoryGroup[] }

export async function getModelFactory(options?: { signal?: AbortSignal }): Promise<ModelFactoryResponse> {
  const { data } = await apiClient.get<ModelFactoryResponse>('/model-factory', { signal: options?.signal })
  return data
}
