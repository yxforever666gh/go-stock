import { API_PATHS } from './api-types.generated'
import type {
  AccountOverview, AnalysisRun, HealthStatus, LivenessStatus,
  Recommendation, RecommendationDetail, SystemVersionStatus,
} from './api-types.generated'

export { API_PATHS }
export type * from './api-types.generated'

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init)
  const payload = await response.json() as T & { error?: string; readiness?: { error?: string } }
  if (!response.ok) throw new Error(payload?.error || payload?.readiness?.error || `Request failed: ${response.status}`)
  return payload
}

function queryPage(limit = 50, offset = 0): string {
  return `?limit=${encodeURIComponent(limit)}&offset=${encodeURIComponent(offset)}`
}

export const apiClient = {
  system: {
    live: () => requestJSON<LivenessStatus>(API_PATHS.getLiveness),
    health: () => requestJSON<HealthStatus>(API_PATHS.getSystemHealth),
    ready: () => requestJSON<SystemVersionStatus>(API_PATHS.getReadiness),
    version: () => requestJSON<SystemVersionStatus>(API_PATHS.getSystemVersion),
  },
  research: {
    analyses: (limit = 50, offset = 0) => requestJSON<AnalysisRun[]>(API_PATHS.listAnalysisRuns + queryPage(limit, offset)),
    analysis: (id: string) => requestJSON<AnalysisRun>(`${API_PATHS.getAnalysisRun}?id=${encodeURIComponent(id)}`),
    recommendations: (limit = 50, offset = 0) => requestJSON<Recommendation[]>(API_PATHS.listRecommendations + queryPage(limit, offset)),
    recommendation: (id: string) => requestJSON<RecommendationDetail>(`${API_PATHS.getRecommendation}?id=${encodeURIComponent(id)}`),
    account: () => requestJSON<AccountOverview>(API_PATHS.getSimulatedAccount),
  },
  eventsWebSocketURL(location: Location = window.location): string {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${protocol}//${location.host}${API_PATHS.connectEventsWebSocket}`
  },
}
