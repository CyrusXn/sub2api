import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AIImageView from '../AIImageView.vue'

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      locale: { value: 'zh-CN' },
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'aiImage.composer.copyName') return `${params?.name} #${params?.index}`
        if (key === 'aiImage.generation.start') return `生成 ${params?.count}`
        if (key === 'aiImage.generation.confirmMessage') return `确认生成 ${params?.count}`
        return key
      },
    }),
  }
})

function mountView() {
  return mount(AIImageView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        ConfirmDialog: {
          props: ['show'],
          emits: ['confirm', 'cancel'],
          template: '<button v-if="show" data-testid="confirm-generation" type="button" @click="$emit(\'confirm\')">confirm</button>',
        },
        HelpTooltip: { template: '<span><slot name="trigger" /></span>' },
        Toggle: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<input type="checkbox" :checked="modelValue" @change="$emit(\'update:modelValue\', $event.target.checked)" />',
        },
        Icon: { props: ['name'], template: '<i :data-icon="name" />' },
      },
    },
  })
}

describe('AIImageView image count', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    Reflect.deleteProperty(window, 'indexedDB')
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ data: [{ b64_json: window.btoa('png') }] }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    )))
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob:ai-image-test'),
    })
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    })
  })

  it('uses a digit-only numeric count input capped at 10', async () => {
    const wrapper = mountView()
    const countInput = wrapper.get<HTMLInputElement>('#ai-image-count')

    expect(countInput.element.tagName).toBe('INPUT')
    expect(countInput.attributes('type')).toBe('number')
    expect(countInput.attributes('inputmode')).toBe('numeric')
    expect(countInput.attributes('min')).toBe('1')
    expect(countInput.attributes('max')).toBe('10')

    await countInput.setValue('12')
    expect(countInput.element.value).toBe('10')

    await countInput.setValue('0')
    expect(countInput.element.value).toBe('1')

    const nonDigitEvent = new KeyboardEvent('keydown', { key: 'e', cancelable: true })
    countInput.element.dispatchEvent(nonDigitEvent)
    expect(nonDigitEvent.defaultPrevented).toBe(true)
  })

  it('creates one independent image request per requested result', async () => {
    const wrapper = mountView()
    await wrapper.get<HTMLInputElement>('#ai-image-api-key').setValue('sk-test')
    await wrapper.get<HTMLTextAreaElement>('#ai-image-free-prompt').setValue('画一只猫')
    await wrapper.get<HTMLInputElement>('#ai-image-count').setValue('4')

    await wrapper.get('button.btn-primary').trigger('click')
    await wrapper.get('[data-testid="confirm-generation"]').trigger('click')
    await vi.waitFor(() => {
      expect(fetch).toHaveBeenCalledTimes(4)
    })
    await flushPromises()

    const bodies = vi.mocked(fetch).mock.calls.map(([, init]) => JSON.parse(String(init?.body)))
    expect(bodies).toHaveLength(4)
    expect(bodies.every((body) => body.n === 1)).toBe(true)
  })
})
