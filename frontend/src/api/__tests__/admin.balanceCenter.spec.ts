import { afterEach, describe, expect, it, vi } from 'vitest'
import { subscribeUpstreamSiteBalanceReminders } from '../admin/balanceCenter'

class TestWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  readyState = TestWebSocket.CONNECTING
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null

  close() {
    this.readyState = 3
    this.onclose?.()
  }
}

describe('subscribeUpstreamSiteBalanceReminders', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('reconnects when the browser returns online and removes its listener on unsubscribe', () => {
    const sockets: TestWebSocket[] = []
    vi.stubGlobal('WebSocket', class extends TestWebSocket {
      constructor() {
        super()
        sockets.push(this)
      }
    })
    const removeOnlineListener = vi.spyOn(window, 'removeEventListener')

    const unsubscribe = subscribeUpstreamSiteBalanceReminders(vi.fn(), {
      reconnectBaseDelayMs: 60_000,
      reconnectMaxDelayMs: 60_000,
    })
    expect(sockets).toHaveLength(1)
    sockets[0].close()
    window.dispatchEvent(new Event('online'))
    expect(sockets).toHaveLength(2)

    unsubscribe()
    expect(removeOnlineListener).toHaveBeenCalledWith('online', expect.any(Function))
  })
})
