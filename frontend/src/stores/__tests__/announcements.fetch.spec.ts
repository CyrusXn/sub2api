import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAnnouncementStore } from '../announcements'
import type { UserAnnouncement } from '@/types'

const { list, markRead } = vi.hoisted(() => ({ list: vi.fn(), markRead: vi.fn() }))
vi.mock('@/api', () => ({ announcementsAPI: { list, markRead } }))

const notice = (id: number): UserAnnouncement => ({
  id, title: `Notice ${id}`, content: 'Content', notify_mode: 'popup',
  created_at: '2026-09-18T00:00:00Z', updated_at: '2026-09-18T00:00:00Z'
})

function pendingList() {
  let resolve!: (value: UserAnnouncement[]) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<UserAnnouncement[]>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
})
afterEach(() => vi.restoreAllMocks())

describe('announcement fetch ownership', () => {
  it('does not restore announcements or popups after logout resets the store', async () => {
    const store = useAnnouncementStore()
    const old = pendingList()
    list.mockReturnValueOnce(old.promise)
    const request = store.fetchAnnouncements()
    store.reset()
    old.resolve([notice(1)])
    await request
    expect(store.announcements).toEqual([])
    expect(store.currentPopup).toBeNull()
    expect(store.loading).toBe(false)
  })

  it('keeps the new session loading when an old request finishes', async () => {
    const store = useAnnouncementStore()
    const old = pendingList()
    const current = pendingList()
    list.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const oldRequest = store.fetchAnnouncements()
    store.reset()
    const currentRequest = store.fetchAnnouncements()
    old.resolve([])
    await oldRequest
    expect(store.loading).toBe(true)
    current.resolve([notice(2)])
    await currentRequest
    expect(store.announcements.map(a => a.id)).toEqual([2])
    expect(store.loading).toBe(false)
  })

  it('keeps the latest forced refresh when responses arrive out of order', async () => {
    const store = useAnnouncementStore()
    const old = pendingList()
    list.mockReturnValueOnce(old.promise).mockResolvedValueOnce([notice(2)])
    const oldRequest = store.fetchAnnouncements()
    await store.fetchAnnouncements(true)
    old.resolve([notice(1)])
    await oldRequest
    expect(store.announcements.map(a => a.id)).toEqual([2])
    expect(store.currentPopup?.id).toBe(2)
  })

  it('does not clear the new session throttle when an old request rejects', async () => {
    const store = useAnnouncementStore()
    const old = pendingList()
    list.mockReturnValueOnce(old.promise).mockResolvedValue([notice(2)])
    const oldRequest = store.fetchAnnouncements()
    store.reset()
    await store.fetchAnnouncements()
    old.reject(new Error('old request failed'))
    await oldRequest
    await store.fetchAnnouncements()
    expect(list).toHaveBeenCalledTimes(2)
    expect(store.announcements.map(a => a.id)).toEqual([2])
  })

  it('still retries a failed current request', async () => {
    const store = useAnnouncementStore()
    list.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([notice(1)])
    await store.fetchAnnouncements()
    await store.fetchAnnouncements()
    expect(list).toHaveBeenCalledTimes(2)
    expect(store.currentPopup?.id).toBe(1)
  })

  it('still throttles ordinary concurrent fetches', async () => {
    const store = useAnnouncementStore()
    const current = pendingList()
    list.mockReturnValueOnce(current.promise)
    const request = store.fetchAnnouncements()
    await store.fetchAnnouncements()
    expect(list).toHaveBeenCalledTimes(1)
    expect(store.loading).toBe(true)
    current.resolve([])
    await request
    expect(store.loading).toBe(false)
  })

  it('keeps every active announcement and sorts newest first by creation time', async () => {
    const notices = Array.from({ length: 25 }, (_, index) => ({
      ...notice(index + 1),
      created_at: new Date(Date.UTC(2026, 0, index + 1)).toISOString(),
    })).reverse()
    list.mockResolvedValueOnce(notices)

    const store = useAnnouncementStore()
    await store.fetchAnnouncements()

    expect(store.announcements).toHaveLength(25)
    expect(store.announcements[0].id).toBe(25)
    expect(store.announcements[store.announcements.length - 1]?.id).toBe(1)
  })

  it('marks displayed popup notices read together and clears their queued popups', async () => {
    const store = useAnnouncementStore()
    const oldest = notice(1)
    const newest = notice(2)
    list.mockResolvedValueOnce([oldest, newest])
    markRead.mockResolvedValue({ message: 'ok' })
    vi.useFakeTimers()

    await store.fetchAnnouncements()
    await store.dismissPopup()
    await vi.advanceTimersByTimeAsync(350)

    expect(markRead).toHaveBeenCalledTimes(2)
    expect(markRead).toHaveBeenNthCalledWith(1, newest.id)
    expect(markRead).toHaveBeenNthCalledWith(2, oldest.id)
    expect(store.currentPopup).toBeNull()
    expect(store.announcements.every((item) => item.read_at)).toBe(true)
    vi.useRealTimers()
  })

  it('does not replay previously shown popup notices in the same session after a read failure', async () => {
    const store = useAnnouncementStore()
    const notices = [notice(1), notice(2)]
    list.mockResolvedValue(notices)
    markRead.mockRejectedValue(new Error('offline'))

    await store.fetchAnnouncements()
    await store.dismissPopup()
    await store.fetchAnnouncements(true)

    expect(store.currentPopup).toBeNull()
    expect(store.announcements.every((item) => !item.read_at)).toBe(true)
  })
})
