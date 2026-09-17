import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import KeyTestModal from '../KeyTestModal.vue'
import Select from '@/components/common/Select.vue'
import zh from '@/i18n/locales/zh'
import type { ApiKey } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params: Record<string, unknown> = {}) => {
      const message = key.split('.').reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], zh)
      return typeof message === 'string' ? message.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? '')) : key
    }
  })
}))

const encoder = new TextEncoder()
let wrapper: VueWrapper | undefined
let stream: ReadableStreamDefaultController<Uint8Array>
let now = 0
let requests: Array<{ url: string; init: RequestInit }>
let modelIds: string[]
const testPrompt = '你好，请简短回复，确认可以正常对话。'
const emitData = (payload: unknown) => stream.enqueue(encoder.encode(`data: ${JSON.stringify(payload)}\n\n`))

async function openModal() {
  wrapper = mount(KeyTestModal, {
    props: {
      apiKey: { name: '测试密钥', key: 'test-only-key', group_id: 7, status: 'active', group: { name: '当前分组' } } as ApiKey,
      baseUrl: 'https://relay.example'
    },
    global: {
      stubs: { Teleport: true, BaseDialog: { template: '<div><button data-test="close" @click="$emit(\'close\')">关闭</button><slot /></div>' } }
    }
  })
  await flushPromises()
  return wrapper
}
const sendButton = () => wrapper!.findAll('button').find(button => button.text() === '测试')!
async function send() {
  await sendButton().trigger('click')
  await flushPromises()
}

beforeEach(() => {
  now = 1000
  requests = []
  modelIds = ['gpt-image-2', 'text-model']
  vi.spyOn(performance, 'now').mockImplementation(() => now)
  vi.stubGlobal('fetch', vi.fn().mockImplementation(async (url: string, init: RequestInit) => {
    requests.push({ url, init })
    if (url.endsWith('/models')) return Response.json({ data: modelIds.map(id => ({ id })) })
    return new Response(new ReadableStream({
      start(controller) {
        stream = controller
        init.signal!.addEventListener('abort', () => controller.error(new DOMException('Aborted', 'AbortError')))
      }
    }), { headers: { 'Content-Type': 'text/event-stream' } })
  }))
})
afterEach(async () => {
  wrapper?.unmount()
  wrapper = undefined
  await flushPromises()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('密钥测试对话', () => {
  it('移除自由输入，使用可搜索下拉框选择实际模型，GPT 默认 sol', async () => {
    modelIds = ['gpt-5.5', 'gpt-5.6-astra', 'gpt-5.6-sol', 'claude-sonnet-test']
    const view = await openModal()
    expect(view.find('textarea').exists()).toBe(false)
    expect(view.find('datalist').exists()).toBe(false)
    expect(view.text()).not.toContain('测试内容')
    expect(view.text()).not.toContain('Shift+Enter')
    expect(view.get('#key-test-model').text()).toBe('gpt-5.6-sol')
    await view.get('#key-test-model').trigger('click')
    expect(view.findAll('[role="option"]')).toHaveLength(4)
    await view.get('input[aria-label="搜索模型"]').setValue('astra')
    expect(view.findAll('[role="option"]')).toHaveLength(1)
    await view.get('[role="option"]').trigger('click')
    expect(view.get('#key-test-model').text()).toBe('gpt-5.6-astra')
    await send()
    expect(view.findAll('button').some(button => button.text() === '停止')).toBe(true)
    expect(view.get('#key-test-model').attributes('disabled')).toBeDefined()
    expect(JSON.parse(requests[1].init.body as string)).toMatchObject({
      model: 'gpt-5.6-astra', messages: [{ role: 'user', content: testPrompt }]
    })
  })

  it.each([
    [['gpt-5.5', 'gpt-5.7-sol'], 'gpt-5.7-sol'],
    [['gpt-5.5'], 'gpt-5.5'],
    [['gpt-image-2', 'claude-sonnet-test'], 'claude-sonnet-test']
  ])('只使用实际模型列表的默认值：%j', async (available, expected) => {
    modelIds = available as string[]
    const view = await openModal()
    expect(view.get('#key-test-model').text()).toBe(expected)
  })

  it('空模型列表和搜索未匹配内容不能发起测试', async () => {
    modelIds = []
    const view = await openModal()
    expect(sendButton().attributes('disabled')).toBeDefined()
    expect(view.text()).toContain('分组未返回模型')
    await view.get('#key-test-model').trigger('click')
    await view.get('input[aria-label="搜索模型"]').setValue('invented-model')
    expect(view.findAll('[role="option"]')).toHaveLength(0)
    expect(requests).toHaveLength(1)
    view.findComponent(Select).vm.$emit('update:modelValue', 'invented-model')
    await flushPromises()
    expect(sendButton().attributes('disabled')).toBeDefined()
  })

  it.each([[600, '首字 600 ms'], [999, '首字 999 ms'], [1000, '首字 1.00 秒']])('首字耗时 %d 毫秒按阈值显示单位', async (milliseconds, expected) => {
    const view = await openModal()
    await send()
    now = 1000 + Number(milliseconds)
    emitData({ choices: [{ delta: { content: '你好' }, finish_reason: 'stop' }] })
    stream.close()
    await flushPromises()
    expect(view.text()).toContain(expected)
  })

  it('首字计时忽略角色和推理片段，保留流式输出，每次只发送固定消息', async () => {
    const view = await openModal()
    expect(view.get('#key-test-model').text()).toBe('text-model')
    await send()
    now = 1300
    emitData({ choices: [{ delta: { role: 'assistant', reasoning_content: 'thinking' } }] })
    await flushPromises()
    expect(view.text()).toContain('首字 —')
    now = 2250
    emitData({ choices: [{ delta: { content: '你好' } }] })
    await flushPromises()
    expect(view.text()).toContain('首字 1.25 秒')
    expect(view.get('[role="log"]').text()).toContain('你好')
    now = 4000
    emitData({ choices: [{ delta: { content: '！' }, finish_reason: 'stop' }] })
    stream.close()
    await flushPromises()
    expect(view.text()).toContain('首字 1.25 秒')
    expect(view.text()).toContain('测试成功 · 当前模型可用')
    expect(sendButton().attributes('disabled')).toBeUndefined()
    await send()
    expect(JSON.parse(requests[2].init.body as string).messages).toEqual([{ role: 'user', content: testPrompt }])
  })

  it('停止保留已输出文字，后续请求不携带半截对话', async () => {
    const view = await openModal()
    await send()
    emitData({ choices: [{ delta: { content: '半截回复' } }] })
    await flushPromises()
    await view.findAll('button').find(button => button.text() === '停止')!.trigger('click')
    await flushPromises()
    expect(view.text()).toContain('半截回复')
    expect(view.text()).toContain('已停止')
    expect(view.text()).not.toContain('测试成功')
    await send()
    expect(JSON.parse(requests[2].init.body as string).messages).toEqual([{ role: 'user', content: testPrompt }])
  })

  it('关闭弹窗会取消未完成的请求', async () => {
    const view = await openModal()
    await send()
    await view.get('[data-test="close"]').trigger('click')
    expect(requests[1].init.signal!.aborted).toBe(true)
    expect(view.emitted('close')).toHaveLength(1)
  })

  it('超时会结束等待，不能显示测试成功', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const view = await openModal()
    await send()
    await vi.advanceTimersByTimeAsync(120000)
    await flushPromises()
    expect(requests[1].init.signal!.aborted).toBe(true)
    expect(view.text()).toContain('请求超时')
    expect(view.text()).not.toContain('测试成功')
    expect(sendButton().attributes('disabled')).toBeUndefined()
  })
})
