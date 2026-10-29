import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import AccountTestModal from '../AccountTestModal.vue'

const { getAvailableModelsMock } = vi.hoisted(() => ({
  getAvailableModelsMock: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels: getAvailableModelsMock
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: { type: [String, Number, Boolean, null], default: '' },
    options: { type: Array, default: () => [] },
    valueKey: { type: String, default: 'value' },
    labelKey: { type: String, default: 'label' }
  },
  emits: ['update:modelValue'],
  template: `
    <select
      v-bind="$attrs"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value)"
    >
      <option
        v-for="option in options"
        :key="option[valueKey]"
        :value="option[valueKey]"
      >
        {{ option[labelKey] }}
      </option>
    </select>
  `
})

const TextAreaStub = defineComponent({
  name: 'TextArea',
  props: {
    modelValue: { type: String, default: '' }
  },
  emits: ['update:modelValue'],
  template: `
    <textarea
      v-bind="$attrs"
      :value="modelValue"
      @input="$emit('update:modelValue', $event.target.value)"
    />
  `
})

function buildAccount() {
  return {
    id: 1,
    name: 'OpenAI OAuth',
    platform: 'openai',
    type: 'oauth',
    status: 'active',
    credentials: {},
    extra: {},
    concurrency: 1,
    priority: 1,
    proxy_id: null,
    auto_pause_on_expired: false
  } as any
}

describe('AccountTestModal', () => {
  it('Enter 发送，Shift+Enter 与输入法确认不发送', async () => {
    const wrapper = mount(AccountTestModal, {
      props: { show: true, account: buildAccount() },
      global: { stubs: { BaseDialog: BaseDialogStub, Select: SelectStub, TextArea: TextAreaStub, Icon: true } }
    })
    await flushPromises()
    const input = wrapper.get('textarea')
    await input.setValue('测试回车')
    await input.trigger('keydown', { key: 'Enter', shiftKey: true })
    await input.trigger('keydown', { key: 'Enter', isComposing: true })
    expect(global.fetch).not.toHaveBeenCalled()
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(global.fetch).toHaveBeenCalledTimes(1)
  })
  const originalFetch = global.fetch

  beforeEach(() => {
    getAvailableModelsMock.mockReset()
    getAvailableModelsMock.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: {
        getReader: () => ({
          read: vi.fn().mockResolvedValue({ done: true, value: undefined })
        })
      }
    } as any)
    localStorage.setItem('auth_token', 'test-token')
  })

  afterEach(() => {
    global.fetch = originalFetch
    localStorage.clear()
  })

  it('posts compact mode for OpenAI compact probe', async () => {
    const wrapper = mount(AccountTestModal, {
      props: {
        show: true,
        account: buildAccount()
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          TextArea: TextAreaStub,
          Icon: true
        }
      }
    })

    await flushPromises()
    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    ;(wrapper.vm as any).testMode = 'compact'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, options] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(options.body)).toMatchObject({
      model_id: 'gpt-5.4',
      mode: 'compact'
    })
  })

  it('defaults the prompt and prefers the latest model for the account platform', async () => {
    getAvailableModelsMock.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' },
      { id: 'gpt-6.1-sol', display_name: 'GPT-6.1 Sol' }
    ])
    const wrapper = mount(AccountTestModal, {
      props: { show: true, account: buildAccount() },
      global: { stubs: { BaseDialog: BaseDialogStub, Select: SelectStub, TextArea: TextAreaStub, Icon: true } }
    })

    await flushPromises()

    expect((wrapper.vm as any).selectedModelId).toBe('gpt-6.1-sol')
    expect((wrapper.vm as any).testPrompt).toBe('你是谁')
  })

  it('posts custom prompt and renders the actual response as an assistant message', async () => {
    const encoder = new TextEncoder()
    const chunks = [
      encoder.encode('data: {"type":"content","text":"我是测试账号返回的内容"}\n\n'),
      encoder.encode('data: {"type":"test_complete","success":true}\n\n')
    ]
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: {
        getReader: () => ({
          read: vi.fn().mockImplementation(() => Promise.resolve(
            chunks.length > 0
              ? { done: false, value: chunks.shift() }
              : { done: true, value: undefined }
          ))
        })
      }
    } as any)
    const wrapper = mount(AccountTestModal, {
      props: { show: true, account: buildAccount() },
      global: { stubs: { BaseDialog: BaseDialogStub, Select: SelectStub, TextArea: TextAreaStub, Icon: true } }
    })

    await flushPromises()
    ;(wrapper.vm as any).testPrompt = '请介绍一下你自己'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    const [, options] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(options.body)).toMatchObject({ prompt: '请介绍一下你自己' })
    expect((wrapper.vm as any).messages).toEqual([
      { role: 'user', content: '请介绍一下你自己' },
      { role: 'assistant', content: '我是测试账号返回的内容' }
    ])
    expect(wrapper.text()).toContain('我是测试账号返回的内容')
  })

  it('renders Chat Completions path status from test SSE', async () => {
    const encoder = new TextEncoder()
    const chunks = [
      encoder.encode('data: {"type":"status","text":"已通过 /v1/chat/completions 验证"}\n\n'),
      encoder.encode('data: {"type":"test_complete","success":true}\n\n')
    ]
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: {
        getReader: () => ({
          read: vi.fn().mockImplementation(() => Promise.resolve(
            chunks.length > 0
              ? { done: false, value: chunks.shift() }
              : { done: true, value: undefined }
          ))
        })
      }
    } as any)

    const wrapper = mount(AccountTestModal, {
      props: {
        show: true,
        account: buildAccount()
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          TextArea: TextAreaStub,
          Icon: true
        }
      }
    })

    await flushPromises()
    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect((wrapper.vm as any).outputLines.map((line: { text: string }) => line.text)).toContain('已通过 /v1/chat/completions 验证')
  })
})
