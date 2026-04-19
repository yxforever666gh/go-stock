const hasWindow = typeof window !== 'undefined'

if (hasWindow) {
  const hasWailsGo = !!window.go?.main?.App
  const hasWailsRuntime = !!window.runtime

  if (!hasWailsRuntime) {
    const listeners = new Map()
    let socket = null
    let reconnectTimer = null

    const inferPlatform = () => {
      const ua = (navigator.userAgent || '').toLowerCase()
      if (ua.includes('windows')) return 'windows'
      if (ua.includes('mac os') || ua.includes('macintosh')) return 'darwin'
      if (ua.includes('linux')) return 'linux'
      return 'web'
    }

    const emitEvent = (eventName, ...args) => {
      const entries = listeners.get(eventName)
      if (!entries || entries.size === 0) return

      for (const entry of Array.from(entries)) {
        try {
          entry.callback(...args)
        } catch (error) {
          console.error(`[web-bridge] event callback failed: ${eventName}`, error)
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
      }
    }

    const offEvents = (...eventNames) => {
      eventNames.filter(Boolean).forEach((eventName) => listeners.delete(eventName))
    }

    const ensureSocket = () => {
      if (socket || !window.location?.host) return

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      socket = new WebSocket(`${protocol}//${window.location.host}/api/ws`)

      socket.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data)
          if (message?.event) {
            emitEvent(message.event, message.payload)
          }
        } catch (error) {
          console.error('[web-bridge] websocket message parse failed', error)
        }
      }

      socket.onclose = () => {
        socket = null
        if (reconnectTimer) {
          clearTimeout(reconnectTimer)
        }
        reconnectTimer = window.setTimeout(() => {
          reconnectTimer = null
          ensureSocket()
        }, 1000)
      }

      // Web mode may be started without the websocket bridge being reachable yet.
      // Avoid polluting the console on transient connection failures.
      socket.onerror = () => {
        if (socket) {
          try {
            socket.close()
          } catch (_) {
            // Ignore close failures on broken websocket handshakes.
          }
        }
      }
    }

    const openURL = (url) => {
      if (!url) return
      window.open(url, '_blank', 'noopener,noreferrer')
    }

    window.runtime = {
      LogPrint: console.log,
      LogTrace: console.debug,
      LogDebug: console.debug,
      LogInfo: console.info,
      LogWarning: console.warn,
      LogError: console.error,
      LogFatal: console.error,
      EventsOnMultiple(eventName, callback, maxCallbacks) {
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
        }
      },
      EventsOff(eventName, ...additionalEventNames) {
        offEvents(eventName, ...additionalEventNames)
      },
      EventsOffAll() {
        listeners.clear()
      },
      EventsEmit(eventName, ...args) {
        emitEvent(eventName, ...args)
      },
      WindowReload() {
        window.location.reload()
      },
      WindowReloadApp() {
        window.location.reload()
      },
      WindowSetAlwaysOnTop() {},
      WindowSetSystemDefaultTheme() {},
      WindowSetLightTheme() {},
      WindowSetDarkTheme() {},
      WindowCenter() {},
      WindowSetTitle(title) {
        if (typeof title === 'string') {
          document.title = title
        }
      },
      WindowFullscreen() {
        return document.documentElement?.requestFullscreen?.()
      },
      WindowUnfullscreen() {
        return document.exitFullscreen?.()
      },
      WindowIsFullscreen() {
        return !!document.fullscreenElement
      },
      WindowGetSize() {
        return { width: window.innerWidth, height: window.innerHeight }
      },
      WindowSetSize() {},
      WindowSetMaxSize() {},
      WindowSetMinSize() {},
      WindowSetPosition() {},
      WindowGetPosition() {
        return { x: window.screenX || 0, y: window.screenY || 0 }
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
        openURL(url)
      },
      Environment() {
        return Promise.resolve({
          platform: inferPlatform(),
          arch: 'web',
          os: inferPlatform(),
        })
      },
      Quit() {
        window.close()
      },
      Hide() {},
      Show() {},
      ClipboardGetText() {
        return navigator.clipboard?.readText?.() ?? Promise.resolve('')
      },
      ClipboardSetText(text) {
        return navigator.clipboard?.writeText?.(text ?? '') ?? Promise.resolve()
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

    ensureSocket()
  }

  if (!hasWailsGo) {
    const downloadBase64 = (filename, mime, contentBase64) => {
      const binary = atob(contentBase64)
      const bytes = new Uint8Array(binary.length)
      for (let index = 0; index < binary.length; index += 1) {
        bytes[index] = binary.charCodeAt(index)
      }
      const blob = new Blob([bytes], { type: mime || 'application/octet-stream' })
      const link = document.createElement('a')
      link.href = URL.createObjectURL(blob)
      link.download = filename || 'download'
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
      URL.revokeObjectURL(link.href)
    }

    const normalizeExportResult = (payload) => {
      if (!payload || typeof payload !== 'object') {
        return payload
      }

      if (payload.mode === 'download' && payload.contentBase64) {
        downloadBase64(payload.filename, payload.mime, payload.contentBase64)
        return payload.filename ? `已下载：${payload.filename}` : '下载成功'
      }

      if (payload.mode === 'server' && payload.path) {
        return payload.path
      }

      return payload
    }

    const postJSON = async (url, payload) => {
      const response = await fetch(url, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
      })
      const result = await response.json()
      if (!response.ok || result?.error) {
        throw new Error(result?.error || `请求失败: ${response.status}`)
      }
      return result
    }

    const rpcInvoke = async (method, args) => {
      const response = await postJSON('/api/rpc', {
        id: `${Date.now()}-${Math.random().toString(16).slice(2)}`,
        method,
        args,
      })
      if (!response.ok) {
        throw new Error(response.error || `调用失败: ${method}`)
      }
      return response.result
    }

    const exportMarkdown = async (stockCode, stockName) => {
      const result = await postJSON('/api/export/markdown', {
        mode: 'download',
        stockCode,
        stockName,
      })
      return normalizeExportResult(result)
    }

    const exportConfig = async () => {
      const result = await postJSON('/api/export/config', { mode: 'download' })
      return normalizeExportResult(result)
    }

    const exportImage = async (name, base64Data) => {
      const result = await postJSON('/api/export/image', {
        mode: 'download',
        name,
        base64Data,
      })
      return normalizeExportResult(result)
    }

    const exportWord = async (filename, base64Data) => {
      const result = await postJSON('/api/export/word', {
        mode: 'download',
        filename,
        base64Data,
      })
      return normalizeExportResult(result)
    }

    const openURL = (url) => {
      window.runtime?.BrowserOpenURL?.(url)
      return Promise.resolve('')
    }

    window.go = window.go || {}
    window.go.main = window.go.main || {}
    window.go.main.App = new Proxy({}, {
      get(_target, prop) {
        if (typeof prop !== 'string') {
          return undefined
        }

        switch (prop) {
          case 'SaveAsMarkdown':
            return exportMarkdown
          case 'ExportConfig':
            return exportConfig
          case 'SaveImage':
            return exportImage
          case 'SaveWordFile':
            return exportWord
          case 'OpenURL':
            return openURL
          default:
            return (...args) => rpcInvoke(prop, args)
        }
      },
    })
  }
}
