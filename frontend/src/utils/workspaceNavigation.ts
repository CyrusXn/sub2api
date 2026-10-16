import type { WorkspaceTab } from '@/api/workspace'

export function getWorkspaceTab(value: unknown): WorkspaceTab {
  return value === 'image' || value === 'video' ? value : 'chat'
}

export function getWorkspaceReturnRoute(query: Record<string, unknown>) {
  // 回跳只开放固定用户页，不能把任意 query 当作重定向授权。
  return query.returnTo === '/workspace'
    ? { path: '/workspace', query: { tab: getWorkspaceTab(query.tab) } }
    : null
}
