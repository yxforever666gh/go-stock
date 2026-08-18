const DEFAULT_EVENTS_PATH = '/api/v1/events/ws'
const DEFAULT_RECONNECT_DELAY_MS = 1000

function inferPlatform(navigatorObject) {
  const userAgent = (navigatorObject?.userAgent || '').toLowerCase()
  if (userAgent.includes('windows')) return 'windows'
  if (userAgent.includes('mac os') || userAgent.includes('macintosh')) return 'darwin'
  if (userAgent.includes('linux')) return 'linux'
  return 'web'
}

function browserGlobals() {
  const windowObject = globalThis.window
  return {
    windowObject,
    documentObject: windowObject?.document ?? globalThis.document,
    navigatorObject: windowObject?.navigator ?? globalThis.navigator,
    WebSocketClass: windowObject?.WebSocket ?? globalThis.WebSocket,
  }
}

export function createBrowserRuntime({
  windowObject,
  documentObject = windowObject?.document,
  navigatorObject = windowObject?.navigator,
  WebSocketClass = windowObject?.WebSocket,
  eventsPath = DEFAULT_EVENTS_PATH,
  reconnectDelayMs = DEFAULT_RECONNECT_DELAY_MS,
} = {}) {
  const listeners = new Map()
  let socket = null
  let reconnectTimer = null
  let disposed = false

  const disconnect = () => {
    if (!socket) return
    const active = socket
    socket = null
    active.onopen = null
    active.onmessage = null
    active.onerror = null
    active.onclose = null
    try { active.close() } catch (_) { /* already closed */ }
  }

  const clearReconnect = () => {
    if (reconnectTimer === null) return
    const clearTimer = windowObject?.clearTimeout?.bind(windowObject) ?? globalThis.clearTimeout
    clearTimer(reconnectTimer)
    reconnectTimer = null
  }

  const stopWhenIdle = () => {
    if (listeners.size !== 0) return
    clearReconnect()
    disconnect()
  }

  const emit = (eventName, ...args) => {
    const entries = listeners.get(eventName)
    if (!entries) return
    for (const entry of [...entries]) {
      try { entry.callback(...args) } catch (error) { globalThis.console?.error?.(`[browser-events] ${eventName}`, error) }
      if (entry.remaining > 0 && --entry.remaining === 0) entries.delete(entry)
    }
    if (entries.size === 0) listeners.delete(eventName)
    stopWhenIdle()
  }

  const canConnect = () => !disposed && listeners.size > 0 && typeof WebSocketClass === 'function' && !!windowObject?.location?.host

  const scheduleReconnect = () => {
    if (!canConnect() || reconnectTimer !== null) return
    const setTimer = windowObject?.setTimeout?.bind(windowObject) ?? globalThis.setTimeout
    reconnectTimer = setTimer(() => {
      reconnectTimer = null
      connect()
    }, reconnectDelayMs)
  }

  const connect = () => {
    if (socket || !canConnect()) return
    const protocol = windowObject.location.protocol === 'https:' ? 'wss:' : 'ws:'
    let next
    try { next = new WebSocketClass(`${protocol}//${windowObject.location.host}${eventsPath}`) } catch (_) {
      scheduleReconnect()
      return
    }
    socket = next
    next.onmessage = (message) => {
      try {
        const payload = JSON.parse(message.data)
        if (payload?.event) emit(payload.event, payload.payload)
      } catch (error) {
        globalThis.console?.error?.('[browser-events] invalid websocket message', error)
      }
    }
    next.onclose = () => {
      if (socket === next) socket = null
      scheduleReconnect()
    }
    next.onerror = () => {
      try { next.close() } catch (_) { /* broken handshake */ }
    }
  }

  const onMultiple = (eventName, callback, maxCallbacks = -1) => {
    if (!eventName || typeof callback !== 'function') return () => {}
    if (!listeners.has(eventName)) listeners.set(eventName, new Set())
    const entry = { callback, remaining: maxCallbacks > 0 ? maxCallbacks : -1 }
    listeners.get(eventName).add(entry)
    connect()
    return () => {
      listeners.get(eventName)?.delete(entry)
      if (listeners.get(eventName)?.size === 0) listeners.delete(eventName)
      stopWhenIdle()
    }
  }

  return {
    EventsOnMultiple: onMultiple,
    EventsOn: (name, callback) => onMultiple(name, callback, -1),
    EventsOnce: (name, callback) => onMultiple(name, callback, 1),
    EventsOff: (...names) => {
      names.filter(Boolean).forEach((name) => listeners.delete(name))
      stopWhenIdle()
    },
    EventsOffAll: () => {
      listeners.clear()
      stopWhenIdle()
    },
    EventsEmit: emit,
    BrowserOpenURL(url) {
      let parsed
      try { parsed = new URL(url, windowObject?.location?.href) } catch (_) { return }
      if (!['http:', 'https:'].includes(parsed.protocol)) return
      const opened = windowObject?.open?.(parsed.href, '_blank', 'noopener,noreferrer')
      if (opened) {
        try { opened.opener = null } catch (_) { /* noopener already applied */ }
      }
    },
    Environment: () => {
      const platform = inferPlatform(navigatorObject)
      return Promise.resolve({ platform, arch: 'web', os: platform })
    },
    WindowFullscreen: () => documentObject?.documentElement?.requestFullscreen?.(),
    WindowUnfullscreen: () => documentObject?.exitFullscreen?.(),
    WindowIsFullscreen: () => !!documentObject?.fullscreenElement,
    WindowReload: () => windowObject?.location?.reload?.(),
    WindowSetTitle: (title) => {
      if (documentObject && typeof title === 'string') documentObject.title = title
    },
    ClipboardGetText: () => navigatorObject?.clipboard?.readText?.() ?? Promise.resolve(''),
    ClipboardSetText: (text) => navigatorObject?.clipboard?.writeText?.(text ?? '') ?? Promise.resolve(),
    dispose() {
      disposed = true
      listeners.clear()
      clearReconnect()
      disconnect()
    },
  }
}

let installedRuntime = null
let installedWindow = null

function currentRuntime() {
  const globals = browserGlobals()
  if (!installedRuntime || installedWindow !== globals.windowObject) {
    installedRuntime?.dispose?.()
    installedRuntime = createBrowserRuntime(globals)
    installedWindow = globals.windowObject
  }
  return installedRuntime
}

export const EventsOnMultiple = (...args) => currentRuntime().EventsOnMultiple(...args)
export const EventsOn = (...args) => currentRuntime().EventsOn(...args)
export const EventsOnce = (...args) => currentRuntime().EventsOnce(...args)
export const EventsOff = (...args) => currentRuntime().EventsOff(...args)
export const EventsOffAll = (...args) => currentRuntime().EventsOffAll(...args)
export const EventsEmit = (...args) => currentRuntime().EventsEmit(...args)
export const BrowserOpenURL = (...args) => currentRuntime().BrowserOpenURL(...args)
export const Environment = (...args) => currentRuntime().Environment(...args)
export const WindowFullscreen = (...args) => currentRuntime().WindowFullscreen(...args)
export const WindowUnfullscreen = (...args) => currentRuntime().WindowUnfullscreen(...args)
export const WindowIsFullscreen = (...args) => currentRuntime().WindowIsFullscreen(...args)
export const WindowReload = (...args) => currentRuntime().WindowReload(...args)
export const WindowSetTitle = (...args) => currentRuntime().WindowSetTitle(...args)
export const ClipboardGetText = (...args) => currentRuntime().ClipboardGetText(...args)
export const ClipboardSetText = (...args) => currentRuntime().ClipboardSetText(...args)
