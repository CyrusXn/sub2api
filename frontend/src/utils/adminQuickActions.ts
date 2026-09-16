export interface AdminQuickAction {
  name: string
  url: string
}

export function defaultAdminQuickActions(): AdminQuickAction[] {
  return [
    { name: '用户管理', url: '/admin/users' },
    { name: '账号管理', url: '/admin/accounts' },
    { name: '余额中心', url: '/admin/balance-center' },
    { name: '使用记录', url: '/admin/usage' },
    { name: '分组管理', url: '/admin/groups' }
  ]
}

export function isSafeQuickActionURL(value: string): boolean {
  const url = value.trim()
  if (!url || url.length > 2048 || url.includes('\\') || Array.from(url).some(char => char.charCodeAt(0) <= 32 || char.charCodeAt(0) === 127)) return false
  if (url.startsWith('/')) return !url.startsWith('//')
  try {
    const parsed = new URL(url)
    return ['http:', 'https:'].includes(parsed.protocol) && !parsed.username && !parsed.password
  } catch {
    return false
  }
}

export function normalizeAdminQuickActions(value: unknown): AdminQuickAction[] {
  if (!Array.isArray(value)) return defaultAdminQuickActions()
  return value.filter((item): item is AdminQuickAction =>
    typeof item?.name === 'string' && item.name.trim().length > 0 &&
    typeof item?.url === 'string' && isSafeQuickActionURL(item.url)
  ).slice(0, 20).map(item => ({ name: item.name.trim(), url: item.url.trim() }))
}

export function clampQuickActionPosition(x: number, y: number, width: number, height: number) {
  return {
    x: Math.max(8, Math.min(x, width - 64)),
    y: Math.max(8, Math.min(y, height - 64))
  }
}
