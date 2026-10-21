import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountTestModal from '../AccountTestModal.vue'
import accountsViewSource from '@/views/admin/AccountsView.vue?raw'

const { getAvailableModels, copyToClipboard } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'admin.accounts.imagePromptDefault': 'Generate a cute orange cat astronaut sticker on a clean pastel background.'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.accounts.imageReceived' && params?.count) {
          return `received-${params.count}`
        }
        if (key === 'admin.accounts.imagePreviewAlt' && params?.index) {
          return `test-image-${params.index}`
        }
        return messages[key] || key
      }
    })
  }
})

function createStreamResponse(lines: string[]) {
  const encoder = new TextEncoder()
  const chunks = lines.map((line) => encoder.encode(line))
  let index = 0

  return {
    ok: true,
    body: {
      getReader: () => ({
        cancel: vi.fn().mockResolvedValue(undefined),
        releaseLock: vi.fn(),
        read: vi.fn().mockImplementation(async () => {
          if (index < chunks.length) {
            return { done: false, value: chunks[index++] }
          }
          return { done: true, value: undefined }
        })
      })
    }
  } as Response
}

function mountModal(account: Record<string, unknown> = {
  id: 42,
  name: 'Gemini Image Test',
  platform: 'gemini',
  type: 'apikey',
  status: 'active'
}) {
  return mount(AccountTestModal, {
    props: {
      show: false,
      account
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Select: { template: '<div class="select-stub"></div>' },
        TextArea: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<textarea class="textarea-stub" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />'
        },
        Icon: true
      }
    }
  })
}

describe('AccountTestModal', () => {
  it.each([
    ['openai', ['gpt-5.6-sol', 'gpt-6-astra', 'gpt-6-sol', 'gpt-6.1-sol'], 'gpt-6.1-sol'],
    ['openai', ['gpt-5.6-sol', 'gpt-6-astra', 'gpt-6-sol'], 'gpt-6-sol'],
    ['openai', ['gpt-5.6-sol', 'gpt-6-astra'], 'gpt-6-astra'],
    ['grok', ['grok-4.5', 'grok-4.6', 'grok-4.7'], 'grok-4.7'],
    ['gemini', ['gemini-3.7-pro', 'gemini-4-pro', 'gemini-3.8-pro'], 'gemini-4-pro'],
    ['anthropic', ['claude-sonnet-4', 'claude-opus-5', 'claude-opus-5.5'], 'claude-opus-5.5'],
  ])('%s 的探针和对话默认最高版本，并保留手选', async (platform, ids, expected) => {
    getAvailableModels.mockResolvedValue((ids as string[]).map(id => ({ id, display_name: id })))
    const wrapper = mountModal({ id: 42, platform, type: 'apikey', name: platform })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect((wrapper.vm as any).selectedModelId).toBe(expected)
    await wrapper.get('[data-test="chat-tab"]').trigger('click')
    expect((wrapper.vm as any).selectedModelId).toBe(expected)
    ;(wrapper.vm as any).selectedModelId = ids[0]
    await wrapper.get('[data-test="probe-tab"]').trigger('click')
    expect((wrapper.vm as any).selectedModelId).toBe(ids[0])
    wrapper.unmount()
  })

  beforeEach(() => {
    getAvailableModels.mockResolvedValue([
      { id: 'gemini-2.0-flash', display_name: 'Gemini 2.0 Flash' },
      { id: 'gemini-2.5-flash-image', display_name: 'Gemini 2.5 Flash Image' },
      { id: 'gemini-3.1-flash-image', display_name: 'Gemini 3.1 Flash Image' }
    ])
    copyToClipboard.mockReset()
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => (key === 'auth_token' ? 'test-token' : null)),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn()
      },
      configurable: true
    })
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gemini-2.5-flash-image"}\n',
        'data: {"type":"image","image_url":"data:image/png;base64,QUJD","mime_type":"image/png"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('账号页实际入口提供默认探针和独立对话 Tab', async () => {
    expect(accountsViewSource).toContain("import AccountTestModal from '@/components/admin/account/AccountTestModal.vue'")
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.get('[data-test="probe-tab"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-test="chat-tab"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('实际对话支持自定义输入和内存多轮，切回探针不发送历史', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-image-2', display_name: '图片' }, { id: 'gpt-6.1-sol', display_name: '文字' }
    ])
    global.fetch = vi.fn().mockImplementation(async () => new Response(
      'data: {"type":"content","text":"实际回复"}\n\ndata: {"type":"test_complete","success":true}\n\n'
    ))
    const wrapper = mountModal({ id: 42, platform: 'openai', type: 'oauth', name: 'OpenAI' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('[data-test="chat-tab"]').trigger('click')
    expect((wrapper.vm as any).modelOptionsForMode.map((model: { id: string }) => model.id)).toEqual(['gpt-6.1-sol'])
    await wrapper.get('textarea').setValue('第一个问题')
    await (wrapper.vm as any).startTest()
    await wrapper.get('textarea').setValue('第二个问题')
    await (wrapper.vm as any).startTest()
    expect(JSON.parse(vi.mocked(global.fetch).mock.calls[1][1]!.body as string)).toMatchObject({
      mode: 'default', messages: [
        { role: 'user', content: '第一个问题' }, { role: 'assistant', content: '实际回复' },
        { role: 'user', content: '第二个问题' }
      ]
    })
    await wrapper.get('[data-test="probe-tab"]').trigger('click')
    await (wrapper.vm as any).startTest()
    expect(JSON.parse(vi.mocked(global.fetch).mock.calls[2][1]!.body as string).messages).toBeUndefined()
    await (wrapper.vm as any).handleClose()
    expect((wrapper.vm as any).messages).toEqual([])
    expect(localStorage.setItem).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('流式请求中锁选择，停止隔离迟到数据并排除半截上下文', async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>
    global.fetch = vi.fn().mockResolvedValue(new Response(new ReadableStream({ start(controller) { stream = controller } })))
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('[data-test="chat-tab"]').trigger('click')
    const pending = (wrapper.vm as any).startTest()
    await flushPromises()
    stream.enqueue(new TextEncoder().encode('data: {"type":"content","text":"半截回复"}\n\n'))
    await flushPromises()
    expect(wrapper.get('[data-test="probe-tab"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('.select-stub').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('半截回复')
    await wrapper.get('[data-test="stop-test"]').trigger('click')
    stream.enqueue(new TextEncoder().encode('data: {"type":"content","text":"迟到回复"}\n\n'))
    stream.close()
    await pending
    expect(wrapper.text()).not.toContain('迟到回复')
    expect((wrapper.vm as any).messages).toEqual([])
    expect((wrapper.vm as any).status).toBe('idle')
    wrapper.unmount()
  })

  it.each([
    'data: {"type":"content","text":"半截回复"}\n\n',
    'data: {"type":"test_complete","success":true}\n\n',
    'data: {"type":"content","text":"半截回复"}\n\ndata: {"type":"error","error":"失败"}\n\n'
  ])('不完整或失败对话不进入后续上下文 %s', async (body) => {
    global.fetch = vi.fn().mockResolvedValue(new Response(body))
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('[data-test="chat-tab"]').trigger('click')
    await (wrapper.vm as any).startTest()
    expect((wrapper.vm as any).status).toBe('error')
    expect((wrapper.vm as any).messages).toEqual([])
    wrapper.unmount()
  })

  it('关闭和切账号清空会话，迟到的模型和请求不能污染新账号', async () => {
    let finish!: (response: Response) => void
    global.fetch = vi.fn().mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('[data-test="chat-tab"]').trigger('click')
    const pending = (wrapper.vm as any).startTest()
    const signal = vi.mocked(global.fetch).mock.calls[0][1]!.signal!
    await wrapper.setProps({ account: { id: 90, platform: 'openai', name: '新账号' } as any })
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect((wrapper.vm as any).messages).toEqual([])
    finish(new Response('data: {"type":"content","text":"旧账号回复"}\n\ndata: {"type":"test_complete","success":true}\n\n'))
    await pending
    expect(wrapper.text()).not.toContain('旧账号回复')
    expect((wrapper.vm as any).status).toBe('idle')
    wrapper.unmount()
  })

  it('模型加载迟到不能覆盖新账号，关闭后也不能恢复选择', async () => {
    let oldModels!: (models: unknown[]) => void
    getAvailableModels.mockReturnValueOnce(new Promise(resolve => { oldModels = resolve }))
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    getAvailableModels.mockResolvedValueOnce([{ id: 'gpt-6.1-sol', display_name: '新模型' }])
    await wrapper.setProps({ account: { id: 90, platform: 'openai', name: '新账号' } as any })
    await flushPromises()
    oldModels([{ id: 'old-model', display_name: '旧模型' }])
    await flushPromises()
    expect((wrapper.vm as any).selectedModelId).toBe('gpt-6.1-sol')
    expect((wrapper.vm as any).availableModels.map((model: { id: string }) => model.id)).toEqual(['gpt-6.1-sol'])
    expect((wrapper.vm as any).loadingModels).toBe(false)
    wrapper.unmount()
  })

  it('对话切模型清空上下文，空输入不触发请求', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-6.1-sol', display_name: 'Sol' }, { id: 'gpt-6-luna', display_name: 'Luna' }
    ])
    global.fetch = vi.fn().mockResolvedValue(new Response('data:{"type":"content","text":"回答"}\n\ndata:{"type":"test_complete","success":true}'))
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('[data-test="chat-tab"]').trigger('click')
    await (wrapper.vm as any).startTest()
    expect((wrapper.vm as any).messages).toHaveLength(2)
    ;(wrapper.vm as any).selectedModelId = 'gpt-6-luna'
    await wrapper.vm.$nextTick()
    expect((wrapper.vm as any).messages).toEqual([])
    await wrapper.get('textarea').setValue('   ')
    await (wrapper.vm as any).startTest()
    expect(global.fetch).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('超时取消读取，显示失败并释放请求锁', async () => {
    global.fetch = vi.fn().mockImplementation(async (_url, options) => new Response(new ReadableStream({
      start(controller) {
        options!.signal!.addEventListener('abort', () => controller.error(new DOMException('aborted', 'AbortError')))
      }
    })))
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.get('[data-test="chat-tab"]').trigger('click')
    vi.useFakeTimers()
    const pending = (wrapper.vm as any).startTest()
    await vi.advanceTimersByTimeAsync(120000)
    await pending
    expect((wrapper.vm as any).status).toBe('error')
    expect((wrapper.vm as any).errorMessage).toBe('admin.accounts.testTimeout')
    expect((wrapper.vm as any).running).toBe(false)
    expect((wrapper.vm as any).messages).toEqual([])
    wrapper.unmount()
  })

  it.each(['image', 'video', 'search', 'tts', 'stt', 'realtime'])('Grok %s 探针仍携带原模式和媒体参数', async (mode) => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-4.3', display_name: 'Text' },
      { id: 'grok-imagine', display_name: 'Image' },
      { id: 'grok-imagine-video', display_name: 'Video' }
    ])
    const wrapper = mountModal({ id: 13, name: 'Grok', platform: 'grok', type: 'oauth' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    ;(wrapper.vm as any).grokTestMode = mode
    await wrapper.vm.$nextTick()
    ;(wrapper.vm as any).uploadImageDataURL = 'data:image/png;base64,QUJD'
    ;(wrapper.vm as any).uploadAudioDataURL = 'data:audio/wav;base64,QUJD'
    await (wrapper.vm as any).startTest()
    const body = JSON.parse(vi.mocked(global.fetch).mock.calls[0][1]!.body as string)
    expect(body.mode).toBe(mode)
    expect(body.messages).toBeUndefined()
    if (mode === 'image' || mode === 'video') {
      expect(body.image_data_url).toBe('data:image/png;base64,QUJD')
      expect(body.model_id).toBe(mode === 'image' ? 'grok-imagine' : 'grok-imagine-video')
    } else {
      expect(body.model_id).toBe('')
      expect(body.image_data_url).toBeUndefined()
    }
    expect(body.audio_data_url).toBe(mode === 'stt' ? 'data:audio/wav;base64,QUJD' : undefined)
    wrapper.unmount()
  })

  it('gemini 图片模型测试会携带提示词并渲染图片预览', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('draw a tiny orange cat astronaut')

    const buttons = wrapper.findAll('button')
    const startButton = buttons.find((button) => button.text().includes('admin.accounts.startTest'))
    expect(startButton).toBeTruthy()

    await startButton!.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'gemini-3.1-flash-image',
      prompt: 'draw a tiny orange cat astronaut'
    })

    const preview = wrapper.find('img[alt="test-image-1"]')
    expect(preview.exists()).toBe(true)
    expect(preview.attributes('src')).toBe('data:image/png;base64,QUJD')
  })

  it('grok 账号测试默认选择 Grok 模型', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-4.3', display_name: 'Grok 4.3' },
      { id: 'grok-build-0.1', display_name: 'Grok Build 0.1' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"grok-4.3"}\n',
        'data: {"type":"content","text":"ok"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 13,
      name: 'Grok Account',
      platform: 'grok',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const buttons = wrapper.findAll('button')
    const startButton = buttons.find((button) => button.text().includes('admin.accounts.startTest'))
    expect(startButton).toBeTruthy()

    await startButton!.trigger('click')
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'grok-4.3',
      prompt: '',
      mode: 'text'
    })
  })

  it('OpenAI Compact 探测会携带 compact 测试模式', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 42,
      name: 'OpenAI OAuth',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    ;(wrapper.vm as any).testMode = 'compact'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({
      model_id: 'gpt-5.4',
      prompt: '',
      mode: 'compact'
    })
  })
})
