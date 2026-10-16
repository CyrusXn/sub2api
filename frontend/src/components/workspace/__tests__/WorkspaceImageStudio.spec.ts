import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkspaceImageStudio from '../WorkspaceImageStudio.vue'

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
  return mount(WorkspaceImageStudio, {
    props: { loading: false, selection: { key: { id: 1, name: '测试', key: 'test-only-key', groupName: '测试分组', platform: 'openai', baseUrl: 'https://relay.example', models: ['gpt-image-2'], allowImageGeneration: true }, model: 'gpt-image-2' } },
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

describe('工作台完整生图', () => {
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

  it('参考图通过加号方框上传、预览并删除，复用 multipart edits', async () => {
    const wrapper = mountView()
    expect(wrapper.get('[data-test="image-reference-add"]').find('[data-icon="plus"]').exists()).toBe(true)
    const file = new File(['png'], 'reference.png', { type: 'image/png' })
    const input = wrapper.get<HTMLInputElement>('input[type="file"]')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
    await input.trigger('change')
    expect(wrapper.get('.reference-tile img').attributes('alt')).toBe('reference.png')
    await wrapper.get('#ai-image-free-prompt').setValue('参考素材画一只猫')
    await wrapper.get('button.btn-primary').trigger('click')
    await wrapper.get('[data-testid="confirm-generation"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.emitted('busy-change')?.at(-1)).toEqual([false]))
    const body = vi.mocked(fetch).mock.calls[0][1]?.body as FormData
    expect(body.get('model')).toBe('gpt-image-2')
    expect((body.get('image') as File).name).toBe(file.name)
    expect(String(vi.mocked(fetch).mock.calls[0][0])).toContain('/images/edits')
    await wrapper.get('.reference-remove').trigger('click')
    expect(wrapper.find('.reference-tile').exists()).toBe(false)
    expect(URL.revokeObjectURL).toHaveBeenCalled()
    wrapper.unmount()
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
    wrapper.unmount()
  })

  it('creates one independent image request per requested result', async () => {
    const wrapper = mountView()
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
    expect(bodies.every((body) => body.n === 1 && body.model === 'gpt-image-2')).toBe(true)
    expect(wrapper.find('#ai-image-api-key').exists()).toBe(false)
    wrapper.unmount()
  })

  it('切换 Key 后重试失败任务使用当前模型', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(new Response('', { status: 500 }))
    const wrapper = mountView()
    await wrapper.get('#ai-image-free-prompt').setValue('画一只猫')
    await wrapper.get('button.btn-primary').trigger('click')
    await wrapper.get('[data-testid="confirm-generation"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.emitted('busy-change')?.at(-1)).toEqual([false]))
    await wrapper.setProps({ selection: { key: { id: 2, name: 'Grok', key: 'test-grok-only', groupName: 'Heavy', platform: 'grok', baseUrl: 'https://relay.example', models: ['grok-imagine-image'], allowImageGeneration: true }, model: 'grok-imagine-image' } })
    const retry = wrapper.findAll('button').find(button => button.text().includes('aiImage.results.retry'))!
    await retry.trigger('click')
    await flushPromises()
    const body = JSON.parse(String(vi.mocked(fetch).mock.calls.at(-1)?.[1]?.body))
    expect(body.model).toBe('grok-imagine-image')
    wrapper.unmount()
  })

  it('迁移后继续读取旧模板、张数与画面设置', async () => {
    localStorage.setItem('sub2api:ai-image:settings:v2', JSON.stringify({ aspectRatio: '9:16', sizeTier: '2K', imagesPerPrompt: 3, historyCollapsed: true }))
    localStorage.setItem('sub2api:ai-image:custom-prompts:v1', JSON.stringify([{ id: 'custom-old', name: '旧模板', prompt: '旧提示词' }]))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('#ai-image-count').element.value).toBe('3')
    expect(wrapper.get<HTMLSelectElement>('#ai-image-size-tier').element.value).toBe('2K')
    expect(wrapper.get<HTMLSelectElement>('#ai-image-ratio').element.value).toBe('9:16')
    expect(wrapper.text()).toContain('旧模板')
    expect(localStorage.getItem('sub2api:ai-image:settings:v2')).not.toContain('test-only-key')
    wrapper.unmount()
  })

  it('批量生图最多两请求并行，完成后继续队列', async () => {
    const pending: Array<() => void> = []
    vi.mocked(fetch).mockImplementation(() => new Promise(resolve => pending.push(() => resolve(Response.json({ data: [{ b64_json: window.btoa('png') }] })))))
    const wrapper = mountView()
    await wrapper.get('#ai-image-free-prompt').setValue('并发测试')
    await wrapper.get('#ai-image-count').setValue('4')
    await wrapper.get('button.btn-primary').trigger('click')
    await wrapper.get('[data-testid="confirm-generation"]').trigger('click')
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect(wrapper.emitted('busy-change')?.at(-1)).toEqual([true])
    pending.splice(0).forEach(resolve => resolve())
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(4))
    pending.splice(0).forEach(resolve => resolve())
    await vi.waitFor(() => expect(wrapper.emitted('busy-change')?.at(-1)).toEqual([false]))
    wrapper.unmount()
  })
})
