import { describe, expect, it } from 'vitest'
import { getWorkspaceReturnRoute, getWorkspaceTab } from '../workspaceNavigation'

describe('工作台导航', () => {
  it('只接受三种 Tab，缺省或不合法值回落对话', () => {
    expect(getWorkspaceTab('video')).toBe('video')
    expect(getWorkspaceTab('image')).toBe('image')
    expect(getWorkspaceTab(['video'])).toBe('chat')
    expect(getWorkspaceTab(undefined)).toBe('chat')
  })
  it('密钥创建后返回原 Tab，其他回跳地址均拒绝', () => {
    expect(getWorkspaceReturnRoute({ returnTo: '/workspace', tab: 'video' })).toEqual({ path: '/workspace', query: { tab: 'video' } })
    expect(getWorkspaceReturnRoute({ returnTo: 'https://external.example', tab: 'video' })).toBeNull()
    expect(getWorkspaceReturnRoute({ returnTo: '/admin/users' })).toBeNull()
    expect(getWorkspaceReturnRoute({})).toBeNull()
  })
})
