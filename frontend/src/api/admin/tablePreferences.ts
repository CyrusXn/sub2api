import { apiClient } from '../client'

export type AdminTableKey = 'users' | 'groups' | 'accounts'

export interface TablePreference {
  table_key: AdminTableKey
  exists: boolean
  hidden_columns: string[]
  column_widths: Record<string, number>
  column_order: string[]
  schema_version: number
  updated_at?: string
}

export interface SaveTablePreferenceRequest {
  hidden_columns: string[]
  column_widths: Record<string, number>
  column_order: string[]
  schema_version: number
}

export async function get(tableKey: AdminTableKey): Promise<TablePreference> {
  const { data } = await apiClient.get<TablePreference>(`/admin/table-preferences/${tableKey}`)
  return data
}

export async function save(
  tableKey: AdminTableKey,
  preference: SaveTablePreferenceRequest
): Promise<TablePreference> {
  const { data } = await apiClient.put<TablePreference>(
    `/admin/table-preferences/${tableKey}`,
    preference
  )
  return data
}

export default { get, save }
