import { API_PATHS } from './api-types.generated'
import type {
  ExportPayload,
  ExportRequest,
  HealthStatus,
  LivenessStatus,
  MarketSummaryStatus,
  StrategyRuntimeStatus,
  SystemVersionStatus,
} from './api-types.generated'

export { API_PATHS }
export type * from './api-types.generated'
export type StrategyRuntimeMode = StrategyRuntimeStatus['mode']
export type ExportMode = NonNullable<ExportRequest['mode']>

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init)
  const payload = await response.json() as T & {
    error?: string
    reason?: string
    readiness?: { error?: string }
  }
  if (!response.ok) {
    throw new Error(
      payload?.reason
      || payload?.error
      || payload?.readiness?.error
      || `Request failed: ${response.status}`,
    )
  }
  return payload
}

function postJSON<T>(url: string, body: unknown): Promise<T> {
  return requestJSON<T>(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

export const apiClient = {
  system: {
    live: () => requestJSON<LivenessStatus>(API_PATHS.getLiveness),
    health: () => requestJSON<HealthStatus>(API_PATHS.getSystemHealth),
    ready: () => requestJSON<SystemVersionStatus>(API_PATHS.getReadiness),
    version: () => requestJSON<SystemVersionStatus>(API_PATHS.getSystemVersion),
  },
  strategy: {
    runtime: () => requestJSON<StrategyRuntimeStatus>(API_PATHS.getStrategyRuntime),
  },
  market: {
    latestSummary: (sinceSeconds?: number) => {
      const query = sinceSeconds && sinceSeconds > 0 ? `?sinceSeconds=${encodeURIComponent(sinceSeconds)}` : ''
      return requestJSON<MarketSummaryStatus>(`${API_PATHS.getLatestMarketSummary}${query}`)
    },
  },
  exports: {
    markdown: (stockCode: string, stockName: string, mode: ExportMode = 'download') =>
      postJSON<ExportPayload>(API_PATHS.exportMarkdown, { mode, stockCode, stockName }),
    config: (mode: ExportMode = 'download') =>
      postJSON<ExportPayload>(API_PATHS.exportConfig, { mode }),
    image: (name: string, base64Data: string, mode: ExportMode = 'download') =>
      postJSON<ExportPayload>(API_PATHS.exportImage, { mode, name, base64Data }),
    word: (filename: string, base64Data: string, mode: ExportMode = 'download') =>
      postJSON<ExportPayload>(API_PATHS.exportWord, { mode, filename, base64Data }),
  },
  eventsWebSocketURL(location: Location = window.location): string {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${protocol}//${location.host}${API_PATHS.connectEventsWebSocket}`
  },
}
