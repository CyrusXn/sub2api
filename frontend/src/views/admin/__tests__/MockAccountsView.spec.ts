import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import MockAccountsView from '../MockAccountsView.vue'

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

const Table = defineComponent({ props: ['data'], emits: ['sort'], template: '<div />' })
const Pagination = defineComponent({ props: ['page', 'pageSize', 'total'], emits: ['update:page', 'update:pageSize'], template: '<div />' })

describe('Mock 账号筛选与分页', () => {
  it('搜索保持账号不变，排序先于分页，重新随机才替换数据', async () => {
    const wrapper = mount(MockAccountsView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
      DataTable: Table, Pagination, Icon: true
    } } })
    const table = wrapper.findComponent(Table)
    const pagination = wrapper.findComponent(Pagination)
    const first = table.props('data')[0]
    expect(table.props('data')).toHaveLength(50)
    expect(pagination.props('total')).toBe(300)
    await wrapper.find('input').setValue(first.name)
    expect(table.props('data')).toEqual([first])
    await wrapper.find('input').setValue('')
    expect(table.props('data')[0]).toBe(first)
    table.vm.$emit('sort', 'id', 'asc')
    await wrapper.vm.$nextTick()
    const firstPageLastId = table.props('data')[49].id
    pagination.vm.$emit('update:page', 2)
    await wrapper.vm.$nextTick()
    expect(table.props('data')[0].id).toBeGreaterThan(firstPageLastId)
    await wrapper.findAll('select')[1]!.setValue('team')
    expect(pagination.props('page')).toBe(1)
    expect(table.props('data').every((row: { credentials: { plan_type: string } }) => row.credentials.plan_type === 'team')).toBe(true)
    await wrapper.findAll('select')[1]!.setValue('all')
    await wrapper.find('button').trigger('click')
    expect(table.props('data').some((row: { name: string }) => row.name === first.name)).toBe(false)
    wrapper.unmount()
  })
})
