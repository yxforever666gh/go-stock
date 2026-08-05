import assert from 'node:assert/strict'
import test from 'node:test'

import {
  BrowserOpenURL,
  createBrowserRuntimeAdapter,
} from './browser-runtime.mjs'

test('browser event listeners can subscribe, emit, unsubscribe, and be removed by name', () => {
  const runtime = createBrowserRuntimeAdapter()
  const received = []
  const unsubscribe = runtime.EventsOn('price', (value) => received.push(value))

  runtime.EventsEmit('price', 10)
  unsubscribe()
  runtime.EventsEmit('price', 11)

  runtime.EventsOn('price', (value) => received.push(value))
  runtime.EventsOff('price')
  runtime.EventsEmit('price', 12)

  assert.deepEqual(received, [10])
})

test('browser URL and environment operations use native browser APIs', async () => {
  const calls = []
  const openedWindow = { opener: 'parent' }
  const runtime = createBrowserRuntimeAdapter({
    windowObject: {
      open: (...args) => {
        calls.push(args)
        return openedWindow
      },
    },
    navigatorObject: { userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)' },
  })

  runtime.BrowserOpenURL('https://example.com/report')
  runtime.BrowserOpenURL('javascript:alert(document.domain)')
  runtime.BrowserOpenURL('data:text/html,blocked')

  assert.deepEqual(calls, [[
    'https://example.com/report',
    '_blank',
    'noopener,noreferrer',
  ]])
  assert.equal(openedWindow.opener, null)
  assert.deepEqual(await runtime.Environment(), {
    platform: 'windows',
    arch: 'web',
    os: 'windows',
  })
})

test('exported operations delegate to an existing Wails runtime', () => {
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const openedURLs = []

  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    writable: true,
    value: {
      runtime: {
        BrowserOpenURL(url) {
          openedURLs.push(url)
          return 'native-result'
        },
      },
    },
  })

  try {
    assert.equal(BrowserOpenURL('https://example.com/native'), 'native-result')
    assert.deepEqual(openedURLs, ['https://example.com/native'])
  } finally {
    if (originalWindow) {
      Object.defineProperty(globalThis, 'window', originalWindow)
    } else {
      delete globalThis.window
    }
  }
})

test('browser events consume the fixed websocket event envelope', () => {
  class FakeWebSocket {
    static instances = []

    constructor(url) {
      this.url = url
      this.closed = false
      FakeWebSocket.instances.push(this)
    }

    close() {
      this.closed = true
    }
  }

  const runtime = createBrowserRuntimeAdapter({
    windowObject: {
      location: {
        host: '127.0.0.1:34115',
        protocol: 'https:',
      },
      WebSocket: FakeWebSocket,
    },
  })
  const received = []
  const unsubscribe = runtime.EventsOn('strategy-status', (payload) => received.push(payload))

  assert.equal(FakeWebSocket.instances.length, 1)
  assert.equal(
    FakeWebSocket.instances[0].url,
    'wss://127.0.0.1:34115/api/v1/events/ws',
  )

  FakeWebSocket.instances[0].onmessage({
    data: JSON.stringify({
      event: 'strategy-status',
      payload: { mode: 'paused' },
    }),
  })
  assert.deepEqual(received, [{ mode: 'paused' }])

  unsubscribe()
  assert.equal(FakeWebSocket.instances[0].closed, true)
})
