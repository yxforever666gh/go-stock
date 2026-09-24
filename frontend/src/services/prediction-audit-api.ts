import {API_PATHS} from './api-types.generated'
import type {
  CreatePredictionReplayRequest,
  PredictionAuditDetail,
  PredictionReplay,
} from './api-types.generated'
import {responseErrorMessage} from './http-error.js'
import {requestJSON, withPath} from './http-client'

export type ReplayModelConfig = {
  ID?: number
  id?: number
  name?: string
  modelName?: string
  disabled?: boolean
  [key: string]: unknown
}

function auditPath(operation: 'get' | 'export', ownerId: string): string {
  const template = operation === 'export' ? API_PATHS.exportPredictionAnalysisRunAudit : API_PATHS.getPredictionAnalysisRunAudit
  return withPath(template, {id: ownerId})
}

export const GetPredictionRunAudit = (ownerId: string): Promise<PredictionAuditDetail> =>
  requestJSON<PredictionAuditDetail>(auditPath('get', ownerId))

export const ListPredictionReplayModelConfigs = (): Promise<ReplayModelConfig[]> =>
  requestJSON<ReplayModelConfig[]>(API_PATHS.listPredictionAIConfigs)

export const CreatePredictionReplay = (sourceOwnerId: string, modelConfigId: number): Promise<PredictionReplay> => {
  const body: CreatePredictionReplayRequest = {sourceOwnerId, modelConfigId}
  return requestJSON<PredictionReplay>(API_PATHS.createPredictionReplay, {method: 'POST', body})
}

export const GetPredictionReplay = (replayId: string): Promise<PredictionReplay> =>
  requestJSON<PredictionReplay>(withPath(API_PATHS.getPredictionReplay, {id: replayId}))

function filenameFromDisposition(value: string | null, fallback: string): string {
  if (!value) return fallback
  const encoded = value.match(/filename\*=UTF-8''([^;]+)/i)?.[1]
  if (encoded) {
    try {
      return decodeURIComponent(encoded)
    } catch (_) {
      return encoded
    }
  }
  return value.match(/filename="?([^";]+)"?/i)?.[1]?.trim() || fallback
}

export async function ExportPredictionRunAudit(ownerId: string): Promise<string> {
  const response = await fetch(auditPath('export', ownerId))
  if (!response.ok) {
    const text = await response.text()
    let payload: unknown = text
    try {
      payload = text ? JSON.parse(text) : null
    } catch (_) {
      // Plain-text server errors are still converted by the common formatter.
    }
    throw new Error(responseErrorMessage(payload, response.status))
  }
  const filename = filenameFromDisposition(
    response.headers.get('Content-Disposition'),
    `prediction-audit-${ownerId}.zip`,
  )
  const url = URL.createObjectURL(await response.blob())
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
  return filename
}
