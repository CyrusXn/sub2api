import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import WorkspaceView from '../WorkspaceView.vue'
import { keysAPI } from '@/api/keys'
import Select from '@/components/common/Select.vue'

vi.mock('@/api/keys', () => ({ keysAPI: { list: vi.fn() } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({
  fetchPublicSettings: async () => ({ api_base_url: 'https://relay.example' })
}) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ locale: { value: 'zh-CN' }, t: (key: string, params?: Record<string, unknown>) => params ? `${key} ${JSON.stringify(params)}` : key })
}))

function fixtures() {
  return [
    { id: 1, name: 'Text', key: 'test-text-key', status: 'active', quota: 0, quota_used: 0, expires_at: null, group: { name: 'Text group', platform: 'openai', status: 'active', allow_image_generation: true } },
    { id: 2, name: 'Grok', key: 'test-grok-key', status: 'active', quota: 0, quota_used: 0, expires_at: null, group: { name: 'SuperGrok Heavy', platform: 'grok', status: 'active', allow_image_generation: true } }
  ]
}
let wrapper: ReturnType<typeof mount> | undefined
async function mountView(path = '/workspace') {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/workspace', component: WorkspaceView }, { path: '/keys', component: { template: '<div />' } },
    { path: '/ai-image', component: { template: '<div />' } }
  ] })
  await router.push(path)
  await router.isReady()
  wrapper = mount(WorkspaceView, { attachTo: document.body, global: { plugins: [router], stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    Transition: true
  } } })
  await flushPromises()
  return { view: wrapper, router }
}
beforeEach(() => {
  vi.mocked(keysAPI.list).mockResolvedValue({ items: fixtures(), pages: 1, total: 2, page: 1, page_size: 100 } as Awaited<ReturnType<typeof keysAPI.list>>)
  vi.stubGlobal('fetch', vi.fn(async (url, init) => {
    if (String(url).endsWith('/models')) {
      const key = (init?.headers as Record<string, string>).Authorization
      return Response.json({ data: (key.includes('grok') ? ['grok-4.7', 'grok-imagine-image', 'grok-imagine-video'] : ['gpt-5.6-sol', 'gpt-image-2']).map(id => ({ id })) })
    }
    return new Response('data: {"choices":[{"delta":{"content":"你好"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n', { headers: { 'Content-Type': 'text/event-stream' } })
  }))
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; document.body.replaceChildren(); vi.clearAllMocks(); vi.unstubAllGlobals() })

const selectedValue = (view: ReturnType<typeof mount>, id: string) => {
  return view.findAllComponents(Select).find(component => component.props('id') === id)?.props('modelValue')
}

describe('创作工作台', () => {
  it('离开生图 Tab 隐藏完整组件并保留表单', async () => {
    const { view } = await mountView('/workspace?tab=image')
    await view.get('#ai-image-free-prompt').setValue('保留生图输入')
    await view.get('[data-tab="chat"]').trigger('click')
    expect(view.get('.studio-shell').isVisible()).toBe(false)
    await view.get('[data-tab="image"]').trigger('click')
    expect(view.get('.studio-shell').isVisible()).toBe(true)
    expect(view.get<HTMLTextAreaElement>('#ai-image-free-prompt').element.value).toBe('保留生图输入')
  })

  it('展开全部 Key 后可选误筛的 Key 和模型且不重新加载', async () => {
    const { view } = await mountView('/workspace?tab=video')
    const count = vi.mocked(fetch).mock.calls.length
    await view.get('#workspace-key').trigger('click')
    document.body.querySelector<HTMLElement>('[data-test="show-all-keys"]')!.click()
    await flushPromises()
    const option = [...document.body.querySelectorAll<HTMLElement>('[role="option"]')].find(item => item.textContent?.includes('Text group'))!
    option.click()
    await flushPromises()
    expect(selectedValue(view, 'workspace-key')).toBe('1')
    expect(selectedValue(view, 'workspace-model')).toBe('gpt-5.6-sol')
    expect(fetch).toHaveBeenCalledTimes(count)
    await view.get('#workspace-prompt').setValue('兜底测试')
    expect(view.get('[data-test="submit"]').attributes('disabled')).toBeUndefined()
  })

  it('密钥下拉按 Tab 能力筛选，底部可展开所有 Key，切 Tab 恢复筛选', async () => {
    const { view } = await mountView()
    await view.get('[data-tab="video"]').trigger('click')
    await view.get('#workspace-key').trigger('click')
    expect([...document.body.querySelectorAll('[role="option"]')].map(item => item.textContent?.trim())).toEqual(['Grok · SuperGrok Heavy'])
    const footer = document.body.querySelector<HTMLElement>('[data-test="show-all-keys"]')!
    expect(footer).not.toBeNull()
    footer.click()
    await flushPromises()
    expect(document.body.querySelectorAll('[role="option"]')).toHaveLength(2)
    await view.get('#workspace-key').trigger('click')
    await view.get('[data-tab="chat"]').trigger('click')
    await view.get('[data-tab="video"]').trigger('click')
    await view.get('#workspace-key').trigger('click')
    expect(document.body.querySelectorAll('[role="option"]')).toHaveLength(1)
    expect(keysAPI.list).toHaveBeenCalledTimes(1)
  })

  it('生图 Tab 内具备完整张数和模板控件，不再跳转旧页面', async () => {
    const { view } = await mountView('/workspace?tab=image')
    expect(view.find('#ai-image-count').exists()).toBe(true)
    expect(view.find('#ai-image-free-prompt').exists()).toBe(true)
    expect(view.text()).toContain('aiImage.prompts.title')
    expect(view.find('[href="/ai-image"]').exists()).toBe(false)
    expect(view.find('[data-test="image-reference-add"]').exists()).toBe(true)
  })

  it('视频 Tab 可填写参考视频地址', async () => {
    const { view } = await mountView('/workspace?tab=video')
    expect(view.find('#workspace-video-reference').exists()).toBe(true)
    await view.get('#workspace-video-reference').setValue('https://cdn.example/reference.mp4')
    expect(view.get<HTMLInputElement>('#workspace-video-reference').element.value).toBe('https://cdn.example/reference.mp4')
  })

  it('Grok参考视频沿用源规格，禁用的时长不阻塞提交', async () => {
    const { view } = await mountView('/workspace?tab=video')
    await view.get('#workspace-prompt').setValue('改变视频风格')
    await view.get('#workspace-duration').setValue('0')
    await view.get('#workspace-video-reference').setValue('https://cdn.example/reference.mp4')
    expect(view.get('[data-test="submit"]').attributes('disabled')).toBeUndefined()
  })

  it('密钥识别失败仍可展开全部后手动输入模型', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 503 })))
    const { view } = await mountView('/workspace?tab=image')
    await view.get('#workspace-key').trigger('click')
    const dropdown = document.body.querySelector<HTMLElement>('.select-dropdown-portal')!
    const search = dropdown.querySelector<HTMLInputElement>('input')!
    search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    await flushPromises()
    expect(document.activeElement).toBe(document.body.querySelector('[data-test="show-all-keys"]'))
    document.body.querySelector<HTMLElement>('[data-test="show-all-keys"]')!.click()
    await flushPromises()
    document.body.querySelector<HTMLElement>('[role="option"]')!.click()
    await flushPromises()
    await view.get('#workspace-model').trigger('click')
    const modelSearch = document.body.querySelector<HTMLInputElement>('.select-search-input')!
    modelSearch.value = 'gpt-image-2'
    modelSearch.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    document.body.querySelector<HTMLElement>('[role="option"]')!.click()
    await flushPromises()
    expect(selectedValue(view, 'workspace-model')).toBe('gpt-image-2')
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('兜底模型历史恢复原选择，收起全部 Key 自动切回时清空上下文', async () => {
    const fallback = { ...fixtures()[0], id: 3, name: '误筛Key', key: 'test-typesafe-only', group: { ...fixtures()[0].group, platform: 'typesafe' } }
    vi.mocked(keysAPI.list).mockResolvedValue({ items: [...fixtures(), fallback], pages: 1, total: 3, page: 1, page_size: 100 } as Awaited<ReturnType<typeof keysAPI.list>>)
    const { view } = await mountView()
    await view.get('#workspace-key').trigger('click')
    document.body.querySelector<HTMLElement>('[data-test="show-all-keys"]')!.click()
    await flushPromises()
    const chooseKey = async (name: string) => {
      const option = [...document.body.querySelectorAll<HTMLElement>('[role="option"]')].find(item => item.textContent?.includes(name))!
      option.click()
      await flushPromises()
    }
    await chooseKey('误筛Key')
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    await view.get('#workspace-key').trigger('click')
    await chooseKey('Grok')
    await view.get('.history-item').trigger('click')
    expect(selectedValue(view, 'workspace-key')).toBe('3')
    expect(selectedValue(view, 'workspace-model')).toBe('gpt-5.6-sol')
    expect(view.findAll('.message')).toHaveLength(2)
    await view.get('#workspace-key').trigger('click')
    document.body.querySelector<HTMLElement>('[data-test="show-all-keys"]')!.click()
    await flushPromises()
    expect(selectedValue(view, 'workspace-key')).toBe('1')
    expect(view.findAll('.message')).toHaveLength(0)
  })

  it('对话初始实际填入你是谁，切 Tab 保留用户修改', async () => {
    const { view } = await mountView()
    expect(view.get<HTMLTextAreaElement>('#workspace-prompt').element.value).toBe('你是谁')
    await view.get('#workspace-prompt').setValue('我的问题')
    await view.get('[data-tab="image"]').trigger('click')
    expect(view.get<HTMLTextAreaElement>('#ai-image-free-prompt').element.value).toBe('')
    await view.get('[data-tab="chat"]').trigger('click')
    expect(view.get<HTMLTextAreaElement>('#workspace-prompt').element.value).toBe('我的问题')
  })

  it('默认官 key 与指定模型，手动切 Key 同样选偏好模型且不重新请求', async () => {
    const items = fixtures().map(item => item.id === 2 ? { ...item, name: '官key' } : item)
    vi.mocked(keysAPI.list).mockResolvedValue({ items, pages: 1, total: 2, page: 1, page_size: 100 } as Awaited<ReturnType<typeof keysAPI.list>>)
    vi.stubGlobal('fetch', vi.fn(async (_url, init) => Response.json({ data: ((init?.headers as Record<string, string>).Authorization.includes('grok') ? ['grok-4', 'grok-4.7'] : ['gpt-5.6-sol', 'gpt-6.1-sol']).map(id => ({ id })) })))
    const { view } = await mountView()
    expect(selectedValue(view, 'workspace-key')).toBe('2')
    expect(selectedValue(view, 'workspace-model')).toBe('grok-4.7')
    const count = vi.mocked(fetch).mock.calls.length
    await view.get('#workspace-key').trigger('click')
    const option = [...document.body.querySelectorAll<HTMLElement>('[role="option"]')].find(item => item.textContent?.includes('Text group'))!
    option.click()
    await flushPromises()
    expect(selectedValue(view, 'workspace-model')).toBe('gpt-6.1-sol')
    expect(fetch).toHaveBeenCalledTimes(count)
  })

  it('Gemini 对话按图四提交并展示含签名的正常文本', async () => {
    const item = { ...fixtures()[0], name: '官key', group: { ...fixtures()[0].group, platform: 'gemini' } }
    vi.mocked(keysAPI.list).mockResolvedValue({ items: [item], pages: 1, total: 1, page: 1, page_size: 100 } as Awaited<ReturnType<typeof keysAPI.list>>)
    vi.stubGlobal('fetch', vi.fn(async url => String(url).endsWith('/models') ? Response.json({ data: ['gemini-4-argon', 'gemini-4-argon-high'].map(id => ({ id })) }) : Response.json({ candidates: [{ content: { role: 'model', parts: [{ text: '我是 Gemini', thoughtSignature: 'test-signature' }] }, finishReason: 'STOP' }] })))
    const { view } = await mountView()
    expect(selectedValue(view, 'workspace-model')).toBe('gemini-4-argon-high')
    expect(view.get('.protocol-hint').text()).toContain('Gemini · generateContent')
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    const call = vi.mocked(fetch).mock.calls.find(([url]) => String(url).includes(':generateContent'))!
    expect(call[0]).toBe('https://relay.example/v1beta/models/gemini-4-argon-high:generateContent')
    expect(JSON.parse(String(call[1]?.body))).toEqual({ contents: [{ role: 'user', parts: [{ text: '你是谁' }] }], generationConfig: { maxOutputTokens: 2048 } })
    expect(view.get('.message.assistant').text()).toContain('我是 Gemini')
  })

  it.each([401, 402, 403, 429, 500])('对话 HTTP %s 展示状态与安全原因，不泄露上游内容', async status => {
    const { view } = await mountView()
    vi.mocked(fetch).mockResolvedValueOnce(new Response('private detail', { status }))
    await view.get('#workspace-prompt').setValue('你是谁')
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    const error = view.get('[role="alert"]').text()
    expect(error).toContain('keys.testModal.httpError')
    expect(error).toContain(String(status))
    expect(error).toContain(status === 500 ? 'keys.testModal.httpOther' : `keys.testModal.http${status}`)
    expect(error).not.toContain('private detail')
    expect(view.findAll('.message')).toHaveLength(0)
  })

  it.each(['invalidResponse', 'networkError'])('对话 %s 显示分类提示', async code => {
    const { view } = await mountView()
    if (code === 'networkError') vi.mocked(fetch).mockRejectedValueOnce(new Error('private detail'))
    else vi.mocked(fetch).mockResolvedValueOnce(new Response('not a stream'))
    await view.get('#workspace-prompt').setValue('你是谁')
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    expect(view.get('[role="alert"]').text()).toBe(`keys.testModal.${code}`)
  })

  it('Tab 自动切换到合适的 Key 和模型，并排除其他能力模型', async () => {
    const { view } = await mountView()
    expect(selectedValue(view, 'workspace-model')).toBe('gpt-5.6-sol')
    await view.get('[data-tab="image"]').trigger('click')
    await flushPromises()
    expect(selectedValue(view, 'workspace-model')).toBe('gpt-image-2')
    expect(view.get('#workspace-model').text()).not.toContain('gpt-5.6-sol')
    await view.get('[data-tab="video"]').trigger('click')
    await flushPromises()
    expect(selectedValue(view, 'workspace-key')).toBe('2')
    expect(selectedValue(view, 'workspace-model')).toBe('grok-imagine-video')
    expect(keysAPI.list).toHaveBeenCalledTimes(1)
  })

  it('手动切换密钥仍只选择当前 Tab 的匹配模型', async () => {
    const { view } = await mountView()
    await view.get('[data-tab="image"]').trigger('click')
    await flushPromises()
    await view.get('#workspace-key').trigger('click')
    const option = [...document.body.querySelectorAll<HTMLElement>('[role="option"]')].find(item => item.textContent?.includes('SuperGrok Heavy'))!
    option.click()
    await flushPromises()
    expect(selectedValue(view, 'workspace-model')).toBe('grok-imagine-image')
    expect(view.get('#workspace-model').text()).not.toContain('grok-4.7')
  })

  it('没有 Key 时跳转密钥菜单，保留当前 Tab', async () => {
    vi.mocked(keysAPI.list).mockResolvedValue({ items: [], pages: 0, total: 0, page: 1, page_size: 100 })
    const { view, router } = await mountView('/workspace?tab=video')
    await view.get('[data-test="create-key"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/keys')
    expect(router.currentRoute.value.query).toMatchObject({ returnTo: '/workspace', tab: 'video' })
  })

  it('多轮对话发送上下文，历史恢复保留完整提示词且不会串到其他 Tab', async () => {
    const { view } = await mountView()
    const prompt = '这是一段超过三十二个字符的完整提示词，用来验证历史记录恢复时不会截断用户原文。'
    await view.get('#workspace-prompt').setValue(prompt)
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    await view.get('#workspace-prompt').setValue('继续')
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    const calls = vi.mocked(fetch).mock.calls.filter(([url]) => String(url).includes('/chat/completions'))
    expect(JSON.parse(String(calls[1][1]?.body)).messages).toEqual([
      { role: 'user', content: prompt }, { role: 'assistant', content: '你好' }, { role: 'user', content: '继续' }
    ])
    await view.get('[data-tab="image"]').trigger('click')
    await flushPromises()
    expect(view.findAll('.history-item')).toHaveLength(0)
    await view.get('[data-tab="chat"]').trigger('click')
    await flushPromises()
    await view.findAll('.history-item')[1].trigger('click')
    expect(view.get<HTMLTextAreaElement>('#workspace-prompt').element.value).toBe(prompt)
  })

  it('卸载后窗口聚焦不会触发新的加载', async () => {
    await mountView()
    wrapper?.unmount(); wrapper = undefined
    const count = vi.mocked(keysAPI.list).mock.calls.length
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(keysAPI.list).toHaveBeenCalledTimes(count)
  })

  it('聚焦、点击页面、切换 Tab 均复用首次加载，不发新请求', async () => {
    const { view } = await mountView()
    const count = vi.mocked(fetch).mock.calls.length
    window.dispatchEvent(new Event('focus'))
    await view.get('h1').trigger('click')
    await flushPromises()
    for (const tab of ['image', 'video', 'chat']) {
      await view.get(`[data-tab="${tab}"]`).trigger('click')
      await flushPromises()
    }
    expect(keysAPI.list).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledTimes(count)
  })

  it('没有 Key 时切换 Tab 也不重复加载', async () => {
    vi.mocked(keysAPI.list).mockResolvedValue({ items: [], pages: 0, total: 0, page: 1, page_size: 100 })
    const { view } = await mountView()
    await view.get('[data-tab="video"]').trigger('click')
    await flushPromises()
    expect(keysAPI.list).toHaveBeenCalledTimes(1)
    expect(fetch).not.toHaveBeenCalled()
    expect(selectedValue(view, 'workspace-key')).toBe('')
  })

  it('主动刷新后保留当前 Key 与模型，更新可选模型列表', async () => {
    const { view } = await mountView('/workspace?tab=video')
    let finish: ((value: Response) => void) | undefined
    vi.mocked(fetch).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    await view.get('.label-row button').trigger('click')
    await flushPromises()
    finish?.(Response.json({ data: [{ id: 'grok-4.7' }] }))
    await flushPromises()
    expect(selectedValue(view, 'workspace-key')).toBe('2')
    expect(selectedValue(view, 'workspace-model')).toBe('grok-imagine-video')
    const models = view.findAllComponents(Select).find(component => component.props('id') === 'workspace-model')!
    expect(models.props('options')).toEqual([{ value: 'grok-imagine-video', label: 'grok-imagine-video' }])
    expect(models.props('disabled')).toBe(false)
    expect(view.get('.key-label-row [role="status"]').text()).toBe('workspace.matched')
  })

  it('API Key 与模型均可搜索，筛选在本地完成', async () => {
    const { view } = await mountView()
    expect(view.findAllComponents(Select)).toHaveLength(2)
    const count = vi.mocked(fetch).mock.calls.length
    await view.get('#workspace-key').trigger('click')
    const search = document.body.querySelector<HTMLInputElement>('.select-search-input')!
    search.value = 'Heavy'
    search.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect([...document.body.querySelectorAll('[role="option"]')].map(option => option.textContent?.trim())).toEqual(['Grok · SuperGrok Heavy'])
    await view.get('#workspace-key').trigger('click')
    await view.get('#workspace-model').trigger('click')
    expect(document.body.querySelector('.select-search-input')).not.toBeNull()
    expect(fetch).toHaveBeenCalledTimes(count)
  })

  it('匹配提示位于 API Key 右侧，不展示两段视频说明', async () => {
    const { view } = await mountView('/workspace?tab=video')
    expect(view.find('.workspace-header [role="status"]').exists()).toBe(false)
    expect(view.get('.key-label-row [role="status"]').text()).toBe('workspace.matched')
    expect(view.text()).not.toContain('workspace.videoHint')
    expect(view.text()).not.toContain('workspace.videoPhaseTwo')
  })

  it('初始加载过程中切 Tab 不会丢失 Key 加载', async () => {
    let finish: ((value: Awaited<ReturnType<typeof keysAPI.list>>) => void) | undefined
    vi.mocked(keysAPI.list).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const { view } = await mountView()
    await view.get('[data-tab="video"]').trigger('click')
    await flushPromises()
    expect(keysAPI.list).toHaveBeenCalledTimes(1)
    expect(view.get('.key-label-row [role="status"]').text()).toBe('workspace.matching')
    finish?.({ items: fixtures(), pages: 1, total: 2, page: 1, page_size: 100 } as Awaited<ReturnType<typeof keysAPI.list>>)
    await flushPromises()
    expect(selectedValue(view, 'workspace-model')).toBe('grok-imagine-video')
  })

  it('新对话请求失败不恢复另一段历史对话', async () => {
    const { view } = await mountView()
    await view.get('#workspace-prompt').setValue('旧对话')
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    await view.get('.new-chat').trigger('click')
    vi.mocked(fetch).mockResolvedValueOnce(new Response('private detail', { status: 403 }))
    await view.get('#workspace-prompt').setValue('新对话')
    await view.get('[data-test="submit"]').trigger('click')
    await flushPromises()
    expect(view.findAll('.message')).toHaveLength(0)
  })
})
