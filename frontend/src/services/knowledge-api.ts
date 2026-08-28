import {API_PATHS} from './api-types.generated'
import type {
  CreateKnowledgeDocumentRequest,
  CreateKnowledgeFromResearchRequest,
  CreateKnowledgeMemoryCandidateRequest,
  KnowledgeDecisionRequest,
  KnowledgeDocumentDetail,
  KnowledgeDocumentPage,
  KnowledgeDocumentVersion,
  KnowledgeMemoryCandidate,
  KnowledgeSearchHit,
  KnowledgeVersionDecisionState,
} from './api-types.generated'
import {requestJSON, withPath, withQuery} from './http-client'

export type KnowledgeDecision = KnowledgeDecisionRequest['decision']
export type KnowledgeOwnerType = CreateKnowledgeFromResearchRequest['sourceOwnerType']

export const KNOWLEDGE_MAX_FILE_BYTES = 10 * 1024 * 1024
export const KNOWLEDGE_ACCEPTED_EXTENSIONS = ['.txt', '.md', '.pdf'] as const

function filenameExtension(filename: string): string {
  const normalized = String(filename || '').trim().toLowerCase()
  const index = normalized.lastIndexOf('.')
  return index >= 0 ? normalized.slice(index) : ''
}

export function validateKnowledgeFile(file: Pick<File, 'name' | 'size'>): string {
  if (!file?.name) return '请选择知识文档'
  if (!KNOWLEDGE_ACCEPTED_EXTENSIONS.includes(filenameExtension(file.name) as typeof KNOWLEDGE_ACCEPTED_EXTENSIONS[number])) {
    return '仅支持 .txt、.md 和可提取文本的 .pdf'
  }
  if (Number(file.size || 0) <= 0) return '文件内容为空'
  if (Number(file.size) > KNOWLEDGE_MAX_FILE_BYTES) return '文件不能超过 10 MiB'
  return ''
}

function inferredMimeType(file: Pick<File, 'name'>): CreateKnowledgeDocumentRequest['mimeType'] {
  const extension = filenameExtension(file.name)
  if (extension === '.pdf') return 'application/pdf'
  if (extension === '.md') return 'text/markdown'
  return 'text/plain'
}

export async function fileToKnowledgeRequest(file: File, title = '', fallbackTitle = true): Promise<CreateKnowledgeDocumentRequest> {
  const error = validateKnowledgeFile(file)
  if (error) throw new Error(error)
  const bytes = new Uint8Array(await file.arrayBuffer())
  let binary = ''
  const chunkSize = 0x8000
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, Math.min(offset + chunkSize, bytes.length)))
  }
  return {
    title: String(title || '').trim() || (fallbackTitle ? file.name.replace(/\.[^.]+$/, '') : ''),
    filename: file.name,
    mimeType: inferredMimeType(file),
    contentBase64: btoa(binary),
  }
}

export const ListKnowledgeDocuments = ({page = 1, pageSize = 20, status, q}: {
  page?: number
  pageSize?: number
  status?: string
  q?: string
} = {}): Promise<KnowledgeDocumentPage> => requestJSON(withQuery(API_PATHS.knowledgeDocuments, {page, pageSize, status, q}))

export const CreateKnowledgeDocument = (body: CreateKnowledgeDocumentRequest): Promise<KnowledgeDocumentDetail> =>
  requestJSON<KnowledgeDocumentDetail>(API_PATHS.createKnowledgeDocument, {method: 'POST', body})

export const UploadKnowledgeDocument = async (file: File, title = ''): Promise<KnowledgeDocumentDetail> =>
  CreateKnowledgeDocument(await fileToKnowledgeRequest(file, title))

export const GetKnowledgeDocument = (id: string): Promise<KnowledgeDocumentDetail> =>
  requestJSON<KnowledgeDocumentDetail>(withPath(API_PATHS.knowledgeDocument, {id}))

export const ListKnowledgeDocumentVersions = (id: string): Promise<KnowledgeDocumentVersion[]> =>
  requestJSON<KnowledgeDocumentVersion[]>(withPath(API_PATHS.knowledgeDocumentVersions, {id}))

export const CreateKnowledgeDocumentVersion = (id: string, body: CreateKnowledgeDocumentRequest): Promise<KnowledgeDocumentDetail> =>
  requestJSON<KnowledgeDocumentDetail>(withPath(API_PATHS.createKnowledgeDocumentVersion, {id}), {method: 'POST', body})

export const UploadKnowledgeDocumentVersion = async (id: string, file: File, title = ''): Promise<KnowledgeDocumentDetail> =>
  CreateKnowledgeDocumentVersion(id, await fileToKnowledgeRequest(file, title, false))

export const DecideKnowledgeVersion = (id: string, decision: KnowledgeDecision, reason = ''): Promise<KnowledgeVersionDecisionState> => {
  const body: KnowledgeDecisionRequest = {decision, reason: String(reason || '').trim()}
  return requestJSON<KnowledgeVersionDecisionState>(withPath(API_PATHS.knowledgeVersionDecision, {id}), {method: 'POST', body})
}

export const SearchKnowledge = (q: string, limit = 20): Promise<KnowledgeSearchHit[]> =>
  requestJSON<KnowledgeSearchHit[]>(withQuery(API_PATHS.knowledgeSearch, {q: String(q || '').trim(), limit}))

export const ListKnowledgeMemoryCandidates = (status?: KnowledgeMemoryCandidate['status']): Promise<KnowledgeMemoryCandidate[]> =>
  requestJSON<KnowledgeMemoryCandidate[]>(withQuery(API_PATHS.knowledgeMemoryCandidates, {status}))

export const CreateKnowledgeMemoryCandidate = (body: CreateKnowledgeMemoryCandidateRequest): Promise<KnowledgeMemoryCandidate> =>
  requestJSON<KnowledgeMemoryCandidate>(API_PATHS.createKnowledgeMemoryCandidate, {method: 'POST', body})

export const DecideKnowledgeMemoryCandidate = (id: string, decision: KnowledgeDecision, reason = ''): Promise<KnowledgeMemoryCandidate> => {
  const body: KnowledgeDecisionRequest = {decision, reason: String(reason || '').trim()}
  return requestJSON<KnowledgeMemoryCandidate>(withPath(API_PATHS.knowledgeMemoryCandidateDecision, {id}), {method: 'POST', body})
}

export const CreateKnowledgeFromResearch = (body: CreateKnowledgeFromResearchRequest): Promise<KnowledgeDocumentDetail> =>
  requestJSON<KnowledgeDocumentDetail>(API_PATHS.knowledgeFromResearch, {method: 'POST', body})
