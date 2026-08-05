import { API_PATHS } from './services/api-types.generated'
import {
  BrowserOpenURL,
  installBrowserRuntimeCompatibility,
} from './services/browser-runtime.mjs'

const hasWindow = typeof window !== 'undefined'

if (hasWindow) {
  const hasWailsGo = !!window.go?.main?.App
  installBrowserRuntimeCompatibility({
    eventsPath: API_PATHS.connectEventsWebSocket,
  })

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
      const result = await postJSON(API_PATHS.exportMarkdown, {
        mode: 'download',
        stockCode,
        stockName,
      })
      return normalizeExportResult(result)
    }

    const exportConfig = async () => {
      const result = await postJSON(API_PATHS.exportConfig, { mode: 'download' })
      return normalizeExportResult(result)
    }

    const exportImage = async (name, base64Data) => {
      const result = await postJSON(API_PATHS.exportImage, {
        mode: 'download',
        name,
        base64Data,
      })
      return normalizeExportResult(result)
    }

    const exportWord = async (filename, base64Data) => {
      const result = await postJSON(API_PATHS.exportWord, {
        mode: 'download',
        filename,
        base64Data,
      })
      return normalizeExportResult(result)
    }

    const openURL = (url) => {
      BrowserOpenURL(url)
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
