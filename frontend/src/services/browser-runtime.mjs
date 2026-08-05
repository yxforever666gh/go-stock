const DEFAULT_EVENTS_PATH = '/api/v1/events/ws'
const DEFAULT_RECONNECT_DELAY_MS = 1000

const browserRuntimeControls = new WeakMap()
let installedBrowserRuntime = null
let installedWindow = null

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

export function createBrowserRuntimeAdapter({
  windowObject,
  documentObject = windowObject?.document,
  navigatorObject = windowObject?.navigator,
  WebSocketClass = windowObject?.WebSocket,
  eventsPath = DEFAULT_EVENTS_PATH,
  reconnectDelayMs = DEFAULT_RECONNECT_DELAY_MS,
} = {}) {
  const listeners = new Map()
  const runtimeConsole = windowObject?.console ?? globalThis.console
  let socket = null
  let reconnectTimer = null
  let disposed = false

  const clearReconnectTimer = () => {
    if (reconnectTimer === null) return
    const clearTimer = windowObject?.clearTimeout?.bind(windowObject) ?? globalThis.clearTimeout
    clearTimer(reconnectTimer)
    reconnectTimer = null
  }

  const disconnectSocket = () => {
    if (!socket) return
    const activeSocket = socket
    socket = null
    activeSocket.onopen = null
    activeSocket.onmessage = null
    activeSocket.onerror = null
    activeSocket.onclose = null
    try {
      activeSocket.close()
    } catch (_) {
      // A failed websocket handshake may already have closed the connection.
    }
  }

  const stopSocketWhenIdle = () => {
    if (listeners.size !== 0) return
    clearReconnectTimer()
    disconnectSocket()
  }

  const emitEvent = (eventName, ...args) => {
    const entries = listeners.get(eventName)
    if (!entries || entries.size === 0) return

    for (const entry of Array.from(entries)) {
      try {
        entry.callback(...args)
      } catch (error) {
        runtimeConsole?.error?.(`[browser-runtime] event callback failed: ${eventName}`, error)
      }

      if (entry.remaining > 0) {
        entry.remaining -= 1
        if (entry.remaining === 0) {
          entries.delete(entry)
        }
      }
    }

    if (entries.size === 0) {
      listeners.delete(eventName)
      stopSocketWhenIdle()
    }
  }

  const hasSocketPrerequisites = () => (
    !disposed
    && listeners.size > 0
    && typeof WebSocketClass === 'function'
    && !!windowObject?.location?.host
  )

  const scheduleReconnect = () => {
    if (!hasSocketPrerequisites() || reconnectTimer !== null) return
    const setTimer = windowObject?.setTimeout?.bind(windowObject) ?? globalThis.setTimeout
    reconnectTimer = setTimer(() => {
      reconnectTimer = null
      ensureSocket()
    }, reconnectDelayMs)
  }

  const ensureSocket = () => {
    if (socket || !hasSocketPrerequisites()) return

    const protocol = windowObject.location.protocol === 'https:' ? 'wss:' : 'ws:'
    let nextSocket
    try {
      nextSocket = new WebSocketClass(`${protocol}//${windowObject.location.host}${eventsPath}`)
    } catch (error) {
      runtimeConsole?.error?.('[browser-runtime] websocket connection failed', error)
      scheduleReconnect()
      return
    }
    socket = nextSocket

    nextSocket.onmessage = (event) => {
      try {
        const message = JSON.parse(event.data)
        if (typeof message?.event === 'string' && message.event) {
          emitEvent(message.event, message.payload)
        }
      } catch (error) {
        runtimeConsole?.error?.('[browser-runtime] websocket message parse failed', error)
      }
    }

    nextSocket.onclose = () => {
      if (socket === nextSocket) {
        socket = null
      }
      scheduleReconnect()
    }

    nextSocket.onerror = () => {
      try {
        nextSocket.close()
      } catch (_) {
        // Ignore close failures on broken websocket handshakes.
      }
    }
  }

  const eventsOnMultiple = (eventName, callback, maxCallbacks) => {
    if (!eventName || typeof callback !== 'function') {
      return () => {}
    }
    if (!listeners.has(eventName)) {
      listeners.set(eventName, new Set())
    }
    const entry = {
      callback,
      remaining: typeof maxCallbacks === 'number' && maxCallbacks > 0 ? maxCallbacks : -1,
    }
    listeners.get(eventName).add(entry)
    ensureSocket()

    return () => {
      const entries = listeners.get(eventName)
      if (!entries) return
      entries.delete(entry)
      if (entries.size === 0) {
        listeners.delete(eventName)
      }
      stopSocketWhenIdle()
    }
  }

  const eventsOff = (...eventNames) => {
    eventNames.filter(Boolean).forEach((eventName) => listeners.delete(eventName))
    stopSocketWhenIdle()
  }

  const runtime = {
    LogPrint: (...args) => runtimeConsole?.log?.(...args),
    LogTrace: (...args) => runtimeConsole?.debug?.(...args),
    LogDebug: (...args) => runtimeConsole?.debug?.(...args),
    LogInfo: (...args) => runtimeConsole?.info?.(...args),
    LogWarning: (...args) => runtimeConsole?.warn?.(...args),
    LogError: (...args) => runtimeConsole?.error?.(...args),
    LogFatal: (...args) => runtimeConsole?.error?.(...args),
    EventsOnMultiple: eventsOnMultiple,
    EventsOn: (eventName, callback) => eventsOnMultiple(eventName, callback, -1),
    EventsOnce: (eventName, callback) => eventsOnMultiple(eventName, callback, 1),
    EventsOff: eventsOff,
    EventsOffAll() {
      listeners.clear()
      stopSocketWhenIdle()
    },
    EventsEmit: emitEvent,
    WindowReload() {
      windowObject?.location?.reload?.()
    },
    WindowReloadApp() {
      windowObject?.location?.reload?.()
    },
    WindowSetAlwaysOnTop() {},
    WindowSetSystemDefaultTheme() {},
    WindowSetLightTheme() {},
    WindowSetDarkTheme() {},
    WindowCenter() {},
    WindowSetTitle(title) {
      if (documentObject && typeof title === 'string') {
        documentObject.title = title
      }
    },
    WindowFullscreen() {
      return documentObject?.documentElement?.requestFullscreen?.()
    },
    WindowUnfullscreen() {
      return documentObject?.exitFullscreen?.()
    },
    WindowIsFullscreen() {
      return !!documentObject?.fullscreenElement
    },
    WindowGetSize() {
      return { width: windowObject?.innerWidth ?? 0, height: windowObject?.innerHeight ?? 0 }
    },
    WindowSetSize() {},
    WindowSetMaxSize() {},
    WindowSetMinSize() {},
    WindowSetPosition() {},
    WindowGetPosition() {
      return { x: windowObject?.screenX ?? 0, y: windowObject?.screenY ?? 0 }
    },
    WindowHide() {},
    WindowShow() {},
    WindowMaximise() {},
    WindowToggleMaximise() {},
    WindowUnmaximise() {},
    WindowIsMaximised() {
      return false
    },
    WindowMinimise() {},
    WindowUnminimise() {},
    WindowSetBackgroundColour() {},
    ScreenGetAll() {
      return []
    },
    WindowIsMinimised() {
      return false
    },
    WindowIsNormal() {
      return true
    },
    BrowserOpenURL(url) {
      if (!url) return
      let parsedURL
      try {
        parsedURL = new URL(url, windowObject?.location?.href)
      } catch (_) {
        return
      }
      if (parsedURL.protocol !== 'http:' && parsedURL.protocol !== 'https:') {
        return
      }
      const openedWindow = windowObject?.open?.(parsedURL.href, '_blank', 'noopener,noreferrer')
      if (openedWindow) {
        try {
          openedWindow.opener = null
        } catch (_) {
          // The noopener feature already protects cross-origin windows that reject assignment.
        }
      }
    },
    Environment() {
      const platform = inferPlatform(navigatorObject)
      return Promise.resolve({ platform, arch: 'web', os: platform })
    },
    Quit() {
      windowObject?.close?.()
    },
    Hide() {},
    Show() {},
    ClipboardGetText() {
      return navigatorObject?.clipboard?.readText?.() ?? Promise.resolve('')
    },
    ClipboardSetText(text) {
      return navigatorObject?.clipboard?.writeText?.(text ?? '') ?? Promise.resolve()
    },
    OnFileDrop() {
      return () => {}
    },
    OnFileDropOff() {},
    CanResolveFilePaths() {
      return false
    },
    ResolveFilePaths(files) {
      return Promise.resolve(files ?? [])
    },
  }

  browserRuntimeControls.set(runtime, {
    dispose() {
      disposed = true
      listeners.clear()
      clearReconnectTimer()
      disconnectSocket()
    },
  })
  return runtime
}

export function installBrowserRuntimeCompatibility(options = {}) {
  const globals = browserGlobals()
  const { windowObject } = globals
  if (!windowObject) return null

  if (windowObject.runtime && !browserRuntimeControls.has(windowObject.runtime)) {
    return windowObject.runtime
  }

  if (!installedBrowserRuntime || installedWindow !== windowObject) {
    browserRuntimeControls.get(installedBrowserRuntime)?.dispose()
    installedBrowserRuntime = createBrowserRuntimeAdapter({ ...globals, ...options })
    installedWindow = windowObject
  }

  if (!windowObject.runtime) {
    windowObject.runtime = installedBrowserRuntime
  }
  return windowObject.runtime
}

function currentRuntime() {
  const windowObject = globalThis.window
  if (windowObject?.runtime) return windowObject.runtime
  if (windowObject) return installBrowserRuntimeCompatibility()

  if (!installedBrowserRuntime || installedWindow) {
    browserRuntimeControls.get(installedBrowserRuntime)?.dispose()
    installedBrowserRuntime = createBrowserRuntimeAdapter()
    installedWindow = null
  }
  return installedBrowserRuntime
}

function callRuntime(method, args) {
  const runtime = currentRuntime()
  const implementation = runtime?.[method]
  if (typeof implementation !== 'function') return undefined
  return implementation.apply(runtime, args)
}

export const LogPrint = (...args) => callRuntime('LogPrint', args)
export const LogTrace = (...args) => callRuntime('LogTrace', args)
export const LogDebug = (...args) => callRuntime('LogDebug', args)
export const LogInfo = (...args) => callRuntime('LogInfo', args)
export const LogWarning = (...args) => callRuntime('LogWarning', args)
export const LogError = (...args) => callRuntime('LogError', args)
export const LogFatal = (...args) => callRuntime('LogFatal', args)
export const EventsOnMultiple = (...args) => callRuntime('EventsOnMultiple', args)
export const EventsOn = (eventName, callback) => EventsOnMultiple(eventName, callback, -1)
export const EventsOnce = (eventName, callback) => EventsOnMultiple(eventName, callback, 1)
export const EventsOff = (...args) => callRuntime('EventsOff', args)
export const EventsOffAll = (...args) => callRuntime('EventsOffAll', args)
export const EventsEmit = (...args) => callRuntime('EventsEmit', args)
export const WindowReload = (...args) => callRuntime('WindowReload', args)
export const WindowReloadApp = (...args) => callRuntime('WindowReloadApp', args)
export const WindowSetAlwaysOnTop = (...args) => callRuntime('WindowSetAlwaysOnTop', args)
export const WindowSetSystemDefaultTheme = (...args) => callRuntime('WindowSetSystemDefaultTheme', args)
export const WindowSetLightTheme = (...args) => callRuntime('WindowSetLightTheme', args)
export const WindowSetDarkTheme = (...args) => callRuntime('WindowSetDarkTheme', args)
export const WindowCenter = (...args) => callRuntime('WindowCenter', args)
export const WindowSetTitle = (...args) => callRuntime('WindowSetTitle', args)
export const WindowFullscreen = (...args) => callRuntime('WindowFullscreen', args)
export const WindowUnfullscreen = (...args) => callRuntime('WindowUnfullscreen', args)
export const WindowIsFullscreen = (...args) => callRuntime('WindowIsFullscreen', args)
export const WindowGetSize = (...args) => callRuntime('WindowGetSize', args)
export const WindowSetSize = (...args) => callRuntime('WindowSetSize', args)
export const WindowSetMaxSize = (...args) => callRuntime('WindowSetMaxSize', args)
export const WindowSetMinSize = (...args) => callRuntime('WindowSetMinSize', args)
export const WindowSetPosition = (...args) => callRuntime('WindowSetPosition', args)
export const WindowGetPosition = (...args) => callRuntime('WindowGetPosition', args)
export const WindowHide = (...args) => callRuntime('WindowHide', args)
export const WindowShow = (...args) => callRuntime('WindowShow', args)
export const WindowMaximise = (...args) => callRuntime('WindowMaximise', args)
export const WindowToggleMaximise = (...args) => callRuntime('WindowToggleMaximise', args)
export const WindowUnmaximise = (...args) => callRuntime('WindowUnmaximise', args)
export const WindowIsMaximised = (...args) => callRuntime('WindowIsMaximised', args)
export const WindowMinimise = (...args) => callRuntime('WindowMinimise', args)
export const WindowUnminimise = (...args) => callRuntime('WindowUnminimise', args)
export const WindowSetBackgroundColour = (...args) => callRuntime('WindowSetBackgroundColour', args)
export const ScreenGetAll = (...args) => callRuntime('ScreenGetAll', args)
export const WindowIsMinimised = (...args) => callRuntime('WindowIsMinimised', args)
export const WindowIsNormal = (...args) => callRuntime('WindowIsNormal', args)
export const BrowserOpenURL = (...args) => callRuntime('BrowserOpenURL', args)
export const Environment = (...args) => callRuntime('Environment', args)
export const Quit = (...args) => callRuntime('Quit', args)
export const Hide = (...args) => callRuntime('Hide', args)
export const Show = (...args) => callRuntime('Show', args)
export const ClipboardGetText = (...args) => callRuntime('ClipboardGetText', args)
export const ClipboardSetText = (...args) => callRuntime('ClipboardSetText', args)
export const OnFileDrop = (...args) => callRuntime('OnFileDrop', args)
export const OnFileDropOff = (...args) => callRuntime('OnFileDropOff', args)
export const CanResolveFilePaths = (...args) => callRuntime('CanResolveFilePaths', args)
export const ResolveFilePaths = (...args) => callRuntime('ResolveFilePaths', args)
