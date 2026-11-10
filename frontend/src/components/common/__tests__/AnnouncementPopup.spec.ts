import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import AnnouncementPopup from '../AnnouncementPopup.vue'
import { useAnnouncementStore } from '@/stores/announcements'

const announcementMarkdownStyles = readFileSync(
  resolve(process.cwd(), 'src/styles/announcement-markdown.css'),
  'utf8',
)

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const announcement = {
  id: 1,
  title: 'Preview announcement',
  content: '## Preview heading\n\n<div>HTML content</div><script>window.__xss = true</script>',
  status: 'draft' as const,
  notify_mode: 'popup' as const,
  targeting: { any_of: [] },
  created_at: '2026-07-24T07:30:00Z',
  updated_at: '2026-07-24T07:30:00Z',
}

describe('AnnouncementPopup', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    document.body.innerHTML = ''
    document.body.style.overflow = ''
  })

  it('renders mixed Markdown and HTML inside the shared styled container', async () => {
    const store = useAnnouncementStore()
    const popup = {
      id: 1,
      title: 'Mixed content announcement',
      content: [
        '## Markdown heading',
        '',
        '<div><h3>HTML heading</h3><ul><li>HTML list item</li></ul></div>',
        '',
        '<table><thead><tr><th>Status</th></tr></thead><tbody><tr><td>OK</td></tr></tbody></table>',
        '<script>window.__announcementXss = true</script>',
      ].join('\n'),
      notify_mode: 'popup' as const,
      created_at: '2026-07-24T07:30:00Z',
      updated_at: '2026-07-24T07:30:00Z',
    }
    store.currentPopup = {
      ...popup,
    }
    store.announcements = [popup]

    const wrapper = mount(AnnouncementPopup)
    await wrapper.vm.$nextTick()

    const content = document.body.querySelector('.markdown-body')
    expect(content?.querySelector('h2')?.textContent).toBe('Markdown heading')
    expect(content?.querySelector('h3')?.textContent).toBe('HTML heading')
    expect(content?.querySelector('li')?.textContent).toBe('HTML list item')
    expect(content?.querySelector('table td')?.textContent).toBe('OK')
    expect(content?.querySelector('script')).toBeNull()

    wrapper.unmount()
  })

  it.each(['h2', 'h3', 'ul', 'li', 'blockquote', 'table', 'th', 'td', 'code'])(
    'loads a shared style rule for mixed-content <%s> elements',
    (element) => {
      expect(announcementMarkdownStyles).toContain(`.markdown-body ${element}`)
    },
  )

  it('previews an admin announcement without marking it as read', async () => {
    const store = useAnnouncementStore()
    const dismissPopup = vi.spyOn(store, 'dismissPopup')
    const wrapper = mount(AnnouncementPopup, {
      props: {
        announcement,
        preview: true,
      },
    })

    expect(document.body.textContent).toContain('Preview announcement')
    expect(document.body.querySelector('.markdown-body h2')?.textContent).toBe('Preview heading')
    expect(document.body.querySelector('.markdown-body script')).toBeNull()
    expect(document.body.textContent).toContain('common.close')

    const dismissButton = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="announcement-popup-dismiss"]',
    )
    expect(dismissButton?.className).toContain('btn-primary')
    dismissButton?.click()
    await wrapper.vm.$nextTick()

    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(dismissPopup).not.toHaveBeenCalled()

    await wrapper.setProps({ announcement: null })
    expect(document.body.style.overflow).toBe('')
    wrapper.unmount()
  })

  it('shows the draft status while previewing a draft announcement', () => {
    const wrapper = mount(AnnouncementPopup, {
      props: {
        announcement: { ...announcement, status: 'draft' },
        preview: true,
      },
    })

    expect(document.body.textContent).toContain('admin.announcements.statusLabels.draft')
    expect(document.body.querySelectorAll('[data-testid="announcement-timeline-item"]')).toHaveLength(1)
    wrapper.unmount()
  })

  it('renders all announcements in newest-first timeline order', async () => {
    const store = useAnnouncementStore()
    const oldest = { ...announcement, id: 1, title: 'Old announcement', created_at: '2026-07-22T00:00:00Z' }
    const newest = { ...announcement, id: 2, title: 'New announcement', created_at: '2026-07-24T00:00:00Z' }
    store.announcements = [oldest, newest]
    store.currentPopup = newest
    const wrapper = mount(AnnouncementPopup)

    const items = [...document.body.querySelectorAll('[data-testid="announcement-timeline-item"]')]
    expect(items.map((item) => item.querySelector('h3')?.textContent)).toEqual([
      'New announcement',
      'Old announcement',
    ])
    expect(document.body.querySelector('.announcement-popup-dialog')?.className).toContain('1280px')
    wrapper.unmount()
  })

  it('does not open automatically for silent announcements without a popup trigger', () => {
    const store = useAnnouncementStore()
    store.announcements = [{ ...announcement, notify_mode: 'silent' }]
    store.currentPopup = null
    const wrapper = mount(AnnouncementPopup)

    expect(document.body.querySelector('.announcement-popup-dialog')).toBeNull()
    wrapper.unmount()
  })

  it('keeps the existing user popup dismissal behavior', async () => {
    const store = useAnnouncementStore()
    store.currentPopup = announcement
    store.announcements = [announcement]
    const dismissPopup = vi.spyOn(store, 'dismissPopup').mockResolvedValue()
    const wrapper = mount(AnnouncementPopup)

    const dismissButton = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="announcement-popup-dismiss"]',
    )
    dismissButton?.click()
    await wrapper.vm.$nextTick()

    expect(dismissPopup).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })
})
