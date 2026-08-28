import {API_PATHS} from './api-types.generated'
import type {
  CreateResearchReplayRequest,
  ResearchAuditDetail,
  ResearchReplay,
} from './api-types.generated'
import {responseErrorMessage} from './http-error.js'
import {requestJSON, withPath} from './http-client'

export type ResearchAuditOwnerType = CreateResearchReplayRequest['sourceOwnerType']

export type ReplayModelConfig = {
  ID?: number
  id?: number
  name?: string
  modelName?: string
  disabled?: boolean
  [key: string]: unknown
}

function auditPath(ownerType: ResearchAuditOwnerType, operation: 'get' | 'export', ownerId: string): string {
  const template = ownerType === 'research2'
    ? (operation === 'export' ? API_PATHS.exportResearch2AnalysisRunAudit : API_PATHS.getResearch2AnalysisRunAudit)
    : (operation === 'export' ? API_PATHS.exportResearchAnalysisRunAudit : API_PATHS.getResearchAnalysisRunAudit)
  return withPath(template, {id: ownerId})
}

export const GetResearchRunAudit = (ownerType: ResearchAuditOwnerType, ownerId: string): Promise<ResearchAuditDetail> =>
  requestJSON<ResearchAuditDetail>(auditPath(ownerType, 'get', ownerId))

export const ListResearchReplayModelConfigs = (): Promise<ReplayModelConfig[]> =>
  requestJSON<ReplayModelConfig[]>(API_PATHS.listAIConfigs)

export const CreateResearchReplay = (
  sourceOwnerType: ResearchAuditOwnerType,
  sourceOwnerId: string,
  modelConfigId: number,
): Promise<ResearchReplay> => {
  const body: CreateResearchReplayRequest = {sourceOwnerType, sourceOwnerId, modelConfigId}
  return requestJSON<ResearchReplay>(API_PATHS.createResearchReplay, {method: 'POST', body})
}

export const GetResearchReplay = (replayId: string): Promise<ResearchReplay> =>
  requestJSON<ResearchReplay>(withPath(API_PATHS.getResearchReplay, {id: replayId}))

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

export async function ExportResearchRunAudit(ownerType: ResearchAuditOwnerType, ownerId: string): Promise<string> {
  const response = await fetch(auditPath(ownerType, 'export', ownerId))
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
    `research-audit-${ownerType}-${ownerId}.zip`,
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
