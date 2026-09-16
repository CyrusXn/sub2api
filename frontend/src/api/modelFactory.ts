import apiClient from './client'
import type { ModelPlazaResponse } from './modelPlaza'

export async function getModelFactory(options?: { signal?: AbortSignal }): Promise<ModelPlazaResponse> {
  const { data } = await apiClient.get<ModelPlazaResponse>('/model-factory', { signal: options?.signal })
  return data
}
