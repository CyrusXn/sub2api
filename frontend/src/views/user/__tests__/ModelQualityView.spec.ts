import { DOMWrapper, mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ModelQualityView from '../ModelQualityView.vue'
import { modelQualityAPI } from '@/api/modelQuality'
import { loadWorkspaceKeys, loadWorkspaceModels } from '@/api/workspace'
import zh from '@/i18n/locales/zh/modelQuality'
import { streamKeyTest } from '@/api/keyTest'
import Select from '@/components/common/Select.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const auth = vi.hoisted(() => ({ isAdmin: false }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/api/modelQuality', async (importOriginal) => ({ ...await importOriginal<typeof import('@/api/modelQuality')>(), modelQualityAPI: { list: vi.fn(), save: vi.fn(), update: vi.fn(), remove: vi.fn(), official: vi.fn(), configureOfficial: vi.fn() } }))
vi.mock('@/api/workspace', () => ({ loadWorkspaceKeys: vi.fn(), loadWorkspaceModels: vi.fn(), workspaceModelsForKey: (key: {models: string[]}) => key.models }))
vi.mock('@/api/keyTest', () => ({ streamKeyTest: vi.fn() }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key.split('.').reduce<unknown>((value, segment) => (value as Record<string, unknown>)?.[segment], zh) || key }) }))
let wrapper: ReturnType<typeof mount>
beforeEach(() => {
 vi.resetAllMocks()
 auth.isAdmin = false
 vi.mocked(modelQualityAPI.save).mockResolvedValue({ id: 99 } as never)
 vi.mocked(modelQualityAPI.update).mockResolvedValue(undefined)
 vi.mocked(modelQualityAPI.official).mockResolvedValue({ enabled: false, model: '' })
 vi.mocked(loadWorkspaceKeys).mockResolvedValue([{ id: 1, name: '我的密钥', groupName: '优选', platform: 'openai', baseUrl: '', key: 'not-a-real-key', models: ['gpt-test'], allowImageGeneration: false }])
 vi.mocked(loadWorkspaceModels).mockResolvedValue()
 vi.mocked(streamKeyTest).mockImplementation(async options => { options.onText('实际回答是 28 个。') })
 vi.mocked(modelQualityAPI.list).mockResolvedValue({ items: [{ id: 1, author: '用户·abcdefgh', group_name: '优选', model: 'gpt-test', theme: 'candy', prompt: '糖果问题', status: 'completed', content: '最少 21 个。', created_at: '2026-11-03T02:00:00Z', account_id: 7, account_name: '上游测试账号' }], total: 1, page: 1, page_size: 12 })
})
afterEach(() => { wrapper?.unmount(); document.body.innerHTML = ''; vi.clearAllMocks() })
async function open() { wrapper = mount(ModelQualityView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } } }); await flushPromises(); return wrapper }
describe('模型智检', () => {
 it('用户搜索合并输入、回到第一页，并可清空搜索恢复记录', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  try {
   const initial = { items: [{ id: 1, author: 'ad***@example.com', group_name: '优选', model: 'gpt-test', theme: 'candy' as const, prompt: '问题', status: 'completed' as const, content: '21', created_at: '2026-11-03T02:00:00Z' }], total: 13, page: 1, page_size: 12 }
   vi.mocked(modelQualityAPI.list).mockResolvedValue(initial)
   const view = await open()
   await view.get('.pagination button:last-child').trigger('click')
   await flushPromises()
   vi.mocked(modelQualityAPI.list).mockClear()
   vi.mocked(modelQualityAPI.list).mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 12 })
   await view.get('#quality-filter-user').setValue('ad')
   await view.get('#quality-filter-user').setValue(' ad***@example.com ')
   await vi.advanceTimersByTimeAsync(299)
   expect(modelQualityAPI.list).not.toHaveBeenCalled()
   await vi.advanceTimersByTimeAsync(1)
   await flushPromises()
   expect(modelQualityAPI.list).toHaveBeenCalledTimes(1)
   expect(modelQualityAPI.list).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, user: 'ad***@example.com' }), expect.anything())
   expect(view.find('.quality-result').exists()).toBe(false)
   await view.get('#quality-filter-user').setValue('')
   await vi.advanceTimersByTimeAsync(300)
   await flushPromises()
   expect(modelQualityAPI.list).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, user: '' }), expect.anything())
   expect(view.find('.quality-result').exists()).toBe(true)
   view.unmount()
  } finally { vi.useRealTimers() }
 })
 it('所有下拉选择均使用始终可搜索的统一组件，少量选项也能搜索', async () => {
  const view = await open()
  expect(view.find('select').exists()).toBe(false)
  expect(view.findAllComponents(Select)).toHaveLength(5)
  for (const select of view.findAllComponents(Select)) {
   expect(select.props('searchable')).toBe(true)
   await select.get('.select-trigger').trigger('click')
   expect(document.body.querySelector('.select-search-input')).not.toBeNull()
   await select.get('.select-trigger').trigger('click')
  }
 })
 it('搜索选择密钥后联动模型，分类、分组、模型和状态筛选发送正确参数', async () => {
  vi.mocked(modelQualityAPI.list).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 12, filters: { groups: ['共享分组'], models: ['shared-model'] } } as never)
  vi.mocked(loadWorkspaceKeys).mockResolvedValue([
   { id: 1, name: '我的密钥', groupName: '优选', platform: 'openai', baseUrl: '', key: 'not-a-real-key', models: ['gpt-test'], allowImageGeneration: false },
   { id: 2, name: '备用密钥', groupName: '备用组', platform: 'openai', baseUrl: '', key: 'not-a-real-key', models: ['gpt-other', 'gpt-search'], allowImageGeneration: false }
  ])
  const view = await open()
  async function searchAndChoose(selector: string, query: string, label: string) {
   await view.get(selector).trigger('click')
   const dropdowns = document.body.querySelectorAll<HTMLElement>('.select-dropdown-portal')
   const dropdown = new DOMWrapper(dropdowns[dropdowns.length - 1])
   await dropdown.get('.select-search-input').setValue(query)
   const options = dropdown.findAll('[role=option]')
   expect(options.map(option => option.text())).toEqual([label])
   await options[0].trigger('click')
   await flushPromises()
  }
  await searchAndChoose('#quality-key', '备用组', '备用密钥 · 备用组')
  expect(view.get('#quality-model').text()).toContain('gpt-other')
  await searchAndChoose('#quality-model', 'search', 'gpt-search')
  expect(view.find('#quality-filter-theme').exists()).toBe(false)
  await view.get('[data-theme=candy]').trigger('click')
  await flushPromises()
  await searchAndChoose('#quality-filter-group', '共享', '共享分组')
  await searchAndChoose('#quality-filter-model', 'shared', 'shared-model')
  await searchAndChoose('#quality-filter-status', '正在', '正在进行')
  expect(modelQualityAPI.list).toHaveBeenLastCalledWith({ page: 1, theme: 'candy', group: '共享分组', model: 'shared-model', status: 'running', user: '' }, expect.anything())
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  expect(streamKeyTest).toHaveBeenCalledWith(expect.objectContaining({ model: 'gpt-search' }))
 })
 it('开始生成只滚动到新记录顶部，后续流式更新不再次滚动', async () => {
  const scrollIntoView = vi.fn()
  const originalScroll = HTMLElement.prototype.scrollIntoView
  HTMLElement.prototype.scrollIntoView = scrollIntoView
  let save!: (value: never) => void
  vi.mocked(modelQualityAPI.save).mockImplementationOnce(() => new Promise(resolve => { save = resolve }))
  let emitText!: (value: string) => void, finish!: () => void
  vi.mocked(streamKeyTest).mockImplementation(options => new Promise<void>(resolve => { emitText = options.onText; finish = resolve }))
  try {
   const view = await open()
   await view.get('[data-theme=candy]').trigger('click')
   await view.get('[data-test=start]').trigger('click')
   await flushPromises()
   expect(scrollIntoView).toHaveBeenCalledTimes(1)
   expect(scrollIntoView.mock.contexts[0]).toBe(view.findAll('.quality-result')[0].element)
   expect(scrollIntoView).toHaveBeenCalledWith({ block: 'start', behavior: 'smooth' })
   save({ id: 99 } as never)
   await flushPromises()
   emitText('**第一段**')
   await flushPromises()
   finish()
   await flushPromises()
   expect(scrollIntoView).toHaveBeenCalledTimes(1)
  } finally {
   HTMLElement.prototype.scrollIntoView = originalScroll
  }
 })
 it('四个主题按顺序展示，普通查询使用卡片，糖果显示真实回答而非图片', async () => {
  const view = await open()
  expect(view.findAll('[role=tab]').map(tab => tab.text())).toEqual(['鹈鹕', '糖果', '官key', '自定义'])
  expect(view.find('.results-grid').exists()).toBe(true)
  expect(view.find('.result-card').exists()).toBe(true)
  expect(view.find('.results-list').exists()).toBe(false)
  expect(view.find('.challenge > span').exists()).toBe(false)
  expect(modelQualityAPI.list).toHaveBeenCalledWith({ page: 1, theme: 'pelican', group: '', model: '', status: '', user: '' }, expect.anything())
  expect(view.text()).toContain('最少 21 个。')
  expect(view.text()).toContain('用户·abcdefgh')
  expect(view.find('iframe').exists()).toBe(false)
  expect(view.text()).not.toContain('上游测试账号')
  expect(view.text()).not.toContain('耗时')
 })
 it('只有官key查询显示列表，返回其他主题恢复卡片', async () => {
  const view = await open()
  await view.get('[data-theme=official]').trigger('click')
  await flushPromises()
  expect(view.find('.results-list').exists()).toBe(true)
  expect(view.find('.result-list-head').exists()).toBe(true)
  expect(view.find('.result-card').exists()).toBe(false)
  for (const theme of ['pelican', 'candy', 'custom']) {
   await view.get(`[data-theme=${theme}]`).trigger('click')
   await flushPromises()
   expect(view.find('.results-grid').exists()).toBe(true)
   expect(view.find('.result-card').exists()).toBe(true)
   expect(view.find('.result-list-head').exists()).toBe(false)
  }
 })
 it('所有分类都筛选对应记录，切换时立即清除旧分类记录', async () => {
  const view = await open()
  for (const theme of ['candy', 'official', 'custom', 'pelican']) {
   let resolve!: (value: never) => void
   vi.mocked(modelQualityAPI.list).mockImplementationOnce(() => new Promise(done => { resolve = done }))
   await view.get(`[data-theme=${theme}]`).trigger('click')
   expect(view.find('.quality-result').exists()).toBe(false)
   expect(modelQualityAPI.list).toHaveBeenLastCalledWith({ page: 1, theme, group: '', model: '', status: '', user: '' }, expect.anything())
   resolve({ items: [], total: 0, page: 1, page_size: 12, filters: { groups: [], models: [] } } as never)
   await flushPromises()
  }
 })
 it('较早分类的延迟响应不能覆盖当前分类', async () => {
  const view = await open()
  let resolveOld!: (value: never) => void
  vi.mocked(modelQualityAPI.list).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
  await view.get('[data-theme=candy]').trigger('click')
  vi.mocked(modelQualityAPI.list).mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 12 })
  await view.get('[data-theme=custom]').trigger('click')
  await flushPromises()
  resolveOld({ items: [{ id: 42, theme: 'candy', model: '过期分类结果', status: 'completed', content: '21' }], total: 1, page: 1, page_size: 12 } as never)
  await flushPromises()
  expect(view.text()).not.toContain('过期分类结果')
  expect(view.find('.quality-result').exists()).toBe(false)
 })
 it('测试后端暂时断开后自动重试，恢复时移除加载错误', async () => {
  vi.useFakeTimers()
  try {
   vi.mocked(modelQualityAPI.list).mockRejectedValueOnce({ status: 500 })
   const view = await open()
   expect(view.get('[role=alert]').text()).toContain('检测记录加载失败')
   await vi.advanceTimersByTimeAsync(10000)
   await flushPromises()
   expect(modelQualityAPI.list).toHaveBeenCalledTimes(2)
   expect(view.find('[role=alert]').exists()).toBe(false)
  } finally { vi.useRealTimers() }
 })
 it('管理员额外看到上游账号', async () => { auth.isAdmin = true; const view = await open(); expect(view.text()).toContain('上游测试账号') })
 it('官key回答上方显示实际调用账号，普通用户连账号占位和删除入口都不可见', async () => {
  auth.isAdmin = true
  vi.mocked(modelQualityAPI.list).mockResolvedValue({ items: [{ id: 1, author: 'ad***@example.com', group_name: '官key分组', model: 'gpt-test', theme: 'official', prompt: '你是谁', status: 'completed', content: '我是模型', created_at: '2026-11-04T08:00:00Z', account_id: 7, account_name: '上游测试账号' }], total: 1, page: 1, page_size: 12 })
  const view = await open()
  await view.get('[data-theme=official]').trigger('click')
  await flushPromises()
  expect(view.get('.answer-header').text()).toContain('上游测试账号')
  expect(view.get('.answer-header').text()).toContain('仅管理员可见')
  expect(view.get('.answer-header').text()).not.toContain('糖果')
  expect(view.find('[data-test=delete-run]').exists()).toBe(true)
  view.unmount()
  auth.isAdmin = false
  const publicView = await open()
  await publicView.get('[data-theme=official]').trigger('click')
  await flushPromises()
  expect(publicView.find('.answer-header').exists()).toBe(false)
  expect(publicView.find('.account-line').exists()).toBe(false)
  expect(publicView.find('[data-test=delete-run]').exists()).toBe(false)
 })
 it('管理员确认后删除记录，失败保留记录并提示，重试成功后刷新共享历史', async () => {
  auth.isAdmin = true
  const view = await open()
  await view.get('[data-test=delete-run]').trigger('click')
  expect(modelQualityAPI.remove).not.toHaveBeenCalled()
  expect(view.getComponent(ConfirmDialog).props('show')).toBe(true)
  vi.mocked(modelQualityAPI.remove).mockRejectedValueOnce(new Error('offline'))
  view.getComponent(ConfirmDialog).vm.$emit('confirm')
  await flushPromises()
  expect(view.find('[data-run-id="1"]').exists()).toBe(true)
  expect(document.body.textContent).toContain('删除失败')
  vi.mocked(modelQualityAPI.remove).mockResolvedValueOnce(undefined)
  vi.mocked(modelQualityAPI.list).mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 12 })
  view.getComponent(ConfirmDialog).vm.$emit('confirm')
  await flushPromises()
  expect(modelQualityAPI.remove).toHaveBeenLastCalledWith(1)
  expect(view.find('[data-run-id="1"]').exists()).toBe(false)
  expect(view.getComponent(ConfirmDialog).props('show')).toBe(false)
 })
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
  expect(view.findAll('.quality-result')[0].text()).toContain('正在进行')
  expect(view.findAll('.quality-result')[0].find('.answer-markdown strong').text()).toBe('回答中')
  expect(modelQualityAPI.save).toHaveBeenCalledWith(expect.objectContaining({ status: 'running', content: '' }))
  expect(modelQualityAPI.update).not.toHaveBeenCalledWith(99, expect.objectContaining({ status: 'completed' }))
  finish()
  await flushPromises()
  expect(view.findAll('.quality-result')[0].text()).toContain('已完成')
  expect(view.findAll('.quality-result')[0].text()).toContain('答案是 21。')
  expect(modelQualityAPI.update).toHaveBeenCalledWith(99, expect.objectContaining({ status: 'completed', content: '**回答中**\n\n答案是 21。' }))
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
  expect(view.find('.quality-result .quality-status.running').exists()).toBe(true)
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
  expect(view.findAll('.quality-result')).toHaveLength(2)
 })
 it('历史轮询返回进行中旧快照时不覆盖当前逐段回答', async () => {
  let emit!: (value: string) => void, finish!: () => void
  vi.mocked(streamKeyTest).mockImplementation(options => new Promise<void>(resolve => { emit = options.onText; finish = resolve }))
  const view = await open()
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  emit('**已经收到第一段**')
  await flushPromises()
  vi.mocked(modelQualityAPI.list).mockResolvedValue({ items: [{ id: 99, author: 'ad***@example.com', group_name: '优选', model: 'gpt-test', theme: 'candy', prompt: '糖果问题', status: 'running', content: '', created_at: '2026-11-04T02:00:00Z' }], total: 1, page: 1, page_size: 12 })
  await view.get('.result-filters .btn').trigger('click')
  await flushPromises()
  expect(view.findAll('.quality-result')).toHaveLength(1)
  expect(view.get('.answer-markdown strong').text()).toBe('已经收到第一段')
  finish(); await flushPromises()
  expect(view.get('.quality-status').classes()).toContain('completed')
 })
 it('Markdown 答案使用块级布局和可横向滚动的糖果表格', async () => {
  const view = await open()
  expect(view.find('.answer-scroll').exists()).toBe(true)
  expect(view.find('.answer-markdown').exists()).toBe(true)
  expect(view.find('.answer-markdown').classes()).toContain('markdown-body')
 })
 it('官key仅管理员配置，普通用户可以查看结果而不能启动他人的定时密钥', async () => {
  const view = await open()
  await view.get('[data-theme=official]').trigger('click')
  expect(view.text()).toContain('每小时')
  expect(view.find('[data-test=official-configure]').exists()).toBe(false)
  expect(view.find('[data-test=start]').exists()).toBe(false)
 })
 it('旧后端不支持进行中记录时，最终兼容保存成功不再显示保存失败', async () => {
  vi.mocked(modelQualityAPI.save).mockRejectedValueOnce({ status: 400 }).mockResolvedValueOnce({ id: 99 } as never)
  const view = await open()
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  expect(modelQualityAPI.save).toHaveBeenCalledTimes(2)
  expect(view.find('[role=alert]').exists()).toBe(false)
 })
 it('进入官key时配置尚未加载，配置返回后仍优先选中绑定密钥', async () => {
  auth.isAdmin = true
  let resolveConfig!: (value: { enabled: boolean; model: string; api_key_id: number }) => void
  vi.mocked(modelQualityAPI.official).mockImplementation(() => new Promise(resolve => { resolveConfig = resolve }))
  vi.mocked(loadWorkspaceKeys).mockResolvedValue([
   { id: 1, name: '其他密钥', groupName: '优选', platform: 'openai', baseUrl: '', key: 'not-a-real-key', models: ['gpt-test'], allowImageGeneration: false },
   { id: 2, name: '已绑定的官密钥', groupName: '官key', platform: 'openai', baseUrl: '', key: 'not-a-real-key', models: ['gpt-official'], allowImageGeneration: false }
  ])
  const view = await open()
  await view.get('[data-theme=official]').trigger('click')
  resolveConfig({ enabled: true, model: 'gpt-official', api_key_id: 2 })
  await flushPromises()
  expect(view.findAllComponents(Select).find(select => select.props('id') === 'quality-key')?.props('modelValue')).toBe(2)
  expect(view.findAllComponents(Select).find(select => select.props('id') === 'quality-model')?.props('modelValue')).toBe('gpt-official')
 })
 it('糖果直接复用文本会话，不依赖专用创建接口，也不向模型透露答案', async () => {
  vi.mocked(modelQualityAPI.save).mockRejectedValue({ status: 404 })
  const view = await open()
  await view.get('[data-theme=candy]').trigger('click')
  await view.get('[data-test=start]').trigger('click')
  await flushPromises()
  expect(modelQualityAPI.save).toHaveBeenCalledTimes(2)
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
