import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import zhDashboard from '@/i18n/locales/zh/dashboard'
import enDashboard from '@/i18n/locales/en/dashboard'

const sourcePath = (path: string) => resolve(process.cwd(), 'src', path)

describe('正式包不包含模拟账号入口', () => {
  it('路由和菜单不再引用模拟账号页面', () => {
    const router = readFileSync(sourcePath('router/index.ts'), 'utf8')
    const sidebar = readFileSync(sourcePath('components/layout/AppSidebar.vue'), 'utf8')
    expect(router).not.toContain('/admin/mock-accounts')
    expect(router).not.toContain('MockAccountsView')
    expect(sidebar).not.toContain('/admin/mock-accounts')
  })

  it('删除仅用于模拟账号页面的数据生成器与组件', () => {
    expect(existsSync(sourcePath('utils/mockAccounts.ts'))).toBe(false)
    expect(existsSync(sourcePath('views/admin/MockAccountsView.vue'))).toBe(false)
  })

  it('首包含等待占位时不再标为真实首字', () => {
    expect(zhDashboard.usage.latencyFirstToken).toBe('首包')
    expect(enDashboard.usage.latencyFirstToken).toBe('Packet')
  })
})
