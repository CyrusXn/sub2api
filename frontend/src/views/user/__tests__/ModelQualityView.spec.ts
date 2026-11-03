import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ModelQualityView from '../ModelQualityView.vue'
import { modelQualityAPI } from '@/api/modelQuality'
import { loadWorkspaceKeys, loadWorkspaceModels } from '@/api/workspace'
import zh from '@/i18n/locales/zh/modelQuality'
import { streamKeyTest } from '@/api/keyTest'

const auth = vi.hoisted(() => ({ isAdmin: false }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/api/modelQuality', async (importOriginal) => ({ ...await importOriginal<typeof import('@/api/modelQuality')>(), modelQualityAPI: { list: vi.fn(), save: vi.fn() } }))
vi.mock('@/api/workspace', () => ({ loadWorkspaceKeys: vi.fn(), loadWorkspaceModels: vi.fn(), workspaceModelsForKey: (key: {models: string[]}) => key.models }))
vi.mock('@/api/keyTest', () => ({ streamKeyTest: vi.fn() }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key.split('.').reduce<unknown>((value, segment) => (value as Record<string, unknown>)?.[segment], zh) || key }) }))
let wrapper: ReturnType<typeof mount>
beforeEach(() => {
 auth.isAdmin = false
 vi.mocked(loadWorkspaceKeys).mockResolvedValue([{ id: 1, name: '我的密钥', groupName: '优选', platform: 'openai', baseUrl: '', key: 'not-a-real-key', models: ['gpt-test'], allowImageGeneration: false }])
 vi.mocked(loadWorkspaceModels).mockResolvedValue()
 vi.mocked(streamKeyTest).mockImplementation(async options => { options.onText('实际回答是 28 个。') })
 vi.mocked(modelQualityAPI.list).mockResolvedValue({ items: [{ id: 1, author: '用户·abcdefgh', group_name: '优选', model: 'gpt-test', theme: 'candy', prompt: '糖果问题', status: 'completed', content: '最少 21 个。', created_at: '2026-11-03T02:00:00Z', account_id: 7, account_name: '上游测试账号' }], total: 1, page: 1, page_size: 12 })
})
afterEach(() => { wrapper?.unmount(); vi.clearAllMocks() })
async function open() { wrapper = mount(ModelQualityView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } } }); await flushPromises(); return wrapper }
describe('模型智检', () => {
 it('只有三个主题，默认查询所有用户结果，糖果显示真实回答而非图片', async () => {
  const view = await open()
  expect(view.findAll('[role=tab]').map(tab => tab.text())).toEqual(['鹈鹕', '糖果', '自定义'])
  expect(view.find('.challenge > span').exists()).toBe(false)
  expect(modelQualityAPI.list).toHaveBeenCalledWith({ page: 1, theme: '', status: '' }, expect.anything())
  expect(view.text()).toContain('最少 21 个。')
  expect(view.text()).toContain('用户·abcdefgh')
  expect(view.find('iframe').exists()).toBe(false)
  expect(view.text()).not.toContain('上游测试账号')
  expect(view.text()).not.toContain('耗时')
 })
 it('管理员额外看到上游账号', async () => { auth.isAdmin = true; const view = await open(); expect(view.text()).toContain('上游测试账号') })
 it('开始检测立即插入进行中记录并流式渲染 Markdown，完成后更新状态', async () => {
  let finish!: () => void
  vi.mocked(streamKeyTest).mockImplementation(async options => {
   options.onText('**回答中**')
   await new Promise<void>(resolve => { finish = resolve })
   options.onText('\n\n答案是 21。')
  })
  const view = await open()
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  expect(view.findAll('.result-card')[0].text()).toContain('正在进行')
  expect(view.findAll('.result-card')[0].find('.answer-markdown strong').text()).toBe('回答中')
  finish()
  await flushPromises()
  expect(view.findAll('.result-card')[0].text()).toContain('已完成')
  expect(view.findAll('.result-card')[0].text()).toContain('答案是 21。')
 })
 it('流式空白不结束等待态，首个正文到达后才显示回答且生成状态保持进行中', async () => {
  let emitText!: (value: string) => void
  let finish!: () => void
  vi.mocked(streamKeyTest).mockImplementation(options => new Promise<void>(resolve => {
   emitText = options.onText
   finish = resolve
  }))
  const view = await open()
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  emitText(' ')
  await flushPromises()
  expect(view.find('.answer-pending').exists()).toBe(true)
  emitText('**回答开始**')
  await flushPromises()
  expect(view.find('.answer-pending').exists()).toBe(false)
  expect(view.find('.result-card .quality-status.running').exists()).toBe(true)
  expect(view.find('.answer-markdown strong').text()).toBe('回答开始')
  finish()
  await flushPromises()
 })
 it('保存已成功但列表暂未读到新记录时保留本地结果', async () => {
  vi.mocked(modelQualityAPI.save).mockResolvedValue({ id: 99 } as never)
  const view = await open()
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  expect(view.text()).toContain('实际回答是 28 个。')
  expect(view.findAll('.result-card')).toHaveLength(2)
 })
 it('Markdown 答案使用块级布局和可横向滚动的糖果表格', async () => {
  const view = await open()
  expect(view.find('.answer-scroll').exists()).toBe(true)
  expect(view.find('.answer-markdown').exists()).toBe(true)
  expect(view.find('.answer-markdown').classes()).toContain('markdown-body')
 })
 it('糖果直接复用文本会话，不依赖专用创建接口，也不向模型透露答案', async () => {
  vi.mocked(modelQualityAPI.save).mockRejectedValue({ status: 404 })
  const view = await open()
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  expect(modelQualityAPI.save).toHaveBeenCalledOnce()
  expect(streamKeyTest).toHaveBeenCalledWith(expect.objectContaining({ apiKey: 'not-a-real-key', model: 'gpt-test', maxTokens: 8192 }))
  const sent = vi.mocked(streamKeyTest).mock.calls[0][0]
  expect(sent.messages[0].content).toContain('五角星形')
  expect(sent.messages[0].content).not.toContain('21')
  expect(view.text()).toContain('实际回答是 28 个。')
 })
 it('共享记录未上线也不阻止标准文本生成，清楚提示共享记录限制', async () => {
  vi.mocked(modelQualityAPI.list).mockRejectedValue({ status: 404 })
  const view = await open()
  expect(view.get('[role=alert]').text()).toContain('共享历史')
  expect(view.get<HTMLButtonElement>('[data-test=start]').element.disabled).toBe(false)
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  expect(streamKeyTest).toHaveBeenCalledOnce()
  expect(view.text()).toContain('实际回答是 28 个。')
 })
})
