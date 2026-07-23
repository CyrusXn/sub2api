/**
 * Common component types
 */

export interface Column {
  key: string
  label: string
  sortable?: boolean
  class?: string
  /** 初始/默认列宽（px），用户拖拽后会被本地持久化覆盖 */
  width?: number
  formatter?: (value: any, row: any) => string
}
