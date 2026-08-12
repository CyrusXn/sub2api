import { onUnmounted, ref } from 'vue'
import { adminAPI, type AdminTableKey } from '@/api/admin'

interface TableLayoutSnapshot {
  hiddenColumns: string[]
  columnWidths: Record<string, number>
  columnOrder: string[]
}

interface LegacyTableLayoutKeys {
  columnWidthStorageKey?: string
  columnOrderStorageKey?: string
}

const cloneWidths = (value: Record<string, number>) => ({ ...value })
const cloneOrder = (value: string[]) => [...value]

const readLegacyObject = (key?: string): Record<string, number> => {
  if (!key) return {}
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(key) || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    return Object.fromEntries(
      Object.entries(parsed).filter((entry): entry is [string, number] =>
        typeof entry[1] === 'number' && Number.isFinite(entry[1]) && entry[1] > 0
      )
    )
  } catch {
    return {}
  }
}

const readLegacyArray = (key?: string): string[] => {
  if (!key) return []
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(key) || '[]')
    return Array.isArray(parsed) ? parsed.filter((value): value is string => typeof value === 'string') : []
  } catch {
    return []
  }
}

// 管理端三张核心列表统一通过数据库保存列布局，本地缓存只用于首次迁移和离线兼容。
export function useAdminTablePreference(tableKey: AdminTableKey) {
  const persistedColumnWidths = ref<Record<string, number>>()
  const persistedColumnOrder = ref<string[]>()
  let current: TableLayoutSnapshot = {
    hiddenColumns: [],
    columnWidths: {},
    columnOrder: []
  }
  let loaded = false
  let saveTimer: ReturnType<typeof setTimeout> | null = null

  const saveCurrent = async () => {
    if (!loaded) return
    try {
      await adminAPI.tablePreferences.save(tableKey, {
        hidden_columns: [...current.hiddenColumns],
        column_widths: cloneWidths(current.columnWidths),
        column_order: cloneOrder(current.columnOrder),
        schema_version: 1
      })
    } catch (error) {
      // 列拖拽可能连续触发，保存失败只记录一次请求错误，避免打断管理员操作。
      console.error(`Failed to save ${tableKey} table preference:`, error)
    }
  }

  const scheduleSave = () => {
    if (!loaded) return
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      saveTimer = null
      void saveCurrent()
    }, 400)
  }

  const load = async (
    fallback: TableLayoutSnapshot,
    legacyKeys: LegacyTableLayoutKeys = {}
  ): Promise<string[] | null> => {
    current = {
      hiddenColumns: [...fallback.hiddenColumns],
      columnWidths: Object.keys(fallback.columnWidths).length
        ? cloneWidths(fallback.columnWidths)
        : readLegacyObject(legacyKeys.columnWidthStorageKey),
      columnOrder: fallback.columnOrder.length
        ? cloneOrder(fallback.columnOrder)
        : readLegacyArray(legacyKeys.columnOrderStorageKey)
    }
    try {
      const preference = await adminAPI.tablePreferences.get(tableKey)
      if (preference.exists) {
        current = {
          hiddenColumns: [...preference.hidden_columns],
          columnWidths: cloneWidths(preference.column_widths),
          columnOrder: cloneOrder(preference.column_order)
        }
        persistedColumnWidths.value = cloneWidths(current.columnWidths)
        persistedColumnOrder.value = cloneOrder(current.columnOrder)
        loaded = true
        return [...current.hiddenColumns]
      }
      persistedColumnWidths.value = cloneWidths(current.columnWidths)
      persistedColumnOrder.value = cloneOrder(current.columnOrder)
      loaded = true
      await saveCurrent()
      return null
    } catch (error) {
      // 数据库暂不可用时继续使用本机布局，后续用户调整仍会再次尝试保存。
      console.error(`Failed to load ${tableKey} table preference:`, error)
      persistedColumnWidths.value = cloneWidths(current.columnWidths)
      persistedColumnOrder.value = cloneOrder(current.columnOrder)
      loaded = true
      return null
    }
  }

  const updateHiddenColumns = (hiddenColumns: string[]) => {
    current.hiddenColumns = [...hiddenColumns]
    scheduleSave()
  }

  const updateColumnWidths = (columnWidths: Record<string, number>) => {
    current.columnWidths = cloneWidths(columnWidths)
    persistedColumnWidths.value = cloneWidths(columnWidths)
    scheduleSave()
  }

  const updateColumnOrder = (columnOrder: string[]) => {
    current.columnOrder = cloneOrder(columnOrder)
    persistedColumnOrder.value = cloneOrder(columnOrder)
    scheduleSave()
  }

  onUnmounted(() => {
    if (!saveTimer) return
    clearTimeout(saveTimer)
    saveTimer = null
    void saveCurrent()
  })

  return {
    persistedColumnWidths,
    persistedColumnOrder,
    load,
    updateHiddenColumns,
    updateColumnWidths,
    updateColumnOrder
  }
}
