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
  // 与管理端权限边界保持一致，禁止继续调用用户接口。
  const { data } = await apiClient.get<ModelFactoryResponse>('/admin/model-factory', { signal: options?.signal })
  return data
}
