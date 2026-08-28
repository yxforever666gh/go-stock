export const KNOWLEDGE_VERSION_STATUSES = Object.freeze(['draft', 'approved', 'rejected', 'superseded'])
export const MEMORY_CANDIDATE_STATUSES = Object.freeze(['draft', 'approved', 'rejected'])

export const KNOWLEDGE_STATUS_OPTIONS = Object.freeze([
  {label: '全部状态', value: ''},
  {label: '草稿', value: 'draft'},
  {label: '已批准', value: 'approved'},
  {label: '已拒绝', value: 'rejected'},
  {label: '已替代', value: 'superseded'},
])

export const MEMORY_STATUS_OPTIONS = Object.freeze([
  {label: '全部状态', value: ''},
  {label: '待审批', value: 'draft'},
  {label: '已批准', value: 'approved'},
  {label: '已拒绝', value: 'rejected'},
])

function objectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {}
}

function stringValue(value) {
  return value === undefined || value === null ? '' : String(value)
}

function numberValue(value, fallback = 0) {
  const number = Number(value)
  return Number.isFinite(number) ? number : fallback
}

export function normalizeKnowledgeVersion(value = {}) {
  const source = objectValue(value)
  return {
    ...source,
    versionId: stringValue(source.versionId || source.id),
    documentId: stringValue(source.documentId),
    versionNo: numberValue(source.versionNo || source.versionNumber || source.version, 0),
    contentText: stringValue(source.contentText || source.content),
    contentSha256: stringValue(source.contentSha256 || source.contentHash || source.sha256),
    mimeType: stringValue(source.mimeType),
    sourceFilename: stringValue(source.sourceFilename || source.filename),
    extractionStatus: stringValue(source.extractionStatus || 'complete'),
    extractionError: stringValue(source.extractionError),
    status: stringValue(source.status || 'draft').toLowerCase(),
    createdBy: stringValue(source.createdBy || source.createdByUserId),
    decisionByUserId: stringValue(source.decisionByUserId || source.decidedBy),
    decisionReason: stringValue(source.decisionReason),
    createdAt: source.createdAt || '',
    decidedAt: source.decidedAt || '',
    updatedAt: source.updatedAt || source.decidedAt || source.createdAt || '',
  }
}

export function sortKnowledgeVersions(values) {
  return (Array.isArray(values) ? values : [])
    .map(normalizeKnowledgeVersion)
    .sort((left, right) => right.versionNo - left.versionNo || String(right.createdAt).localeCompare(String(left.createdAt)))
}

export function normalizeKnowledgeDocument(value = {}) {
  const source = objectValue(value)
  const document = objectValue(source.document || source)
  const versions = sortKnowledgeVersions(source.versions || document.versions)
  const explicitLatestVersion = normalizeKnowledgeVersion(source.latestVersion || document.latestVersion)
  const summaryLatestVersion = normalizeKnowledgeVersion({
    versionNumber: document.latestVersionNumber,
    status: document.latestStatus,
    sourceFilename: document.latestSourceFilename,
    contentSha256: document.latestContentSha256,
    createdAt: document.updatedAt,
  })
  const latestVersion = versions[0] || (explicitLatestVersion.versionId ? explicitLatestVersion : summaryLatestVersion.versionNo ? summaryLatestVersion : null)
  const approvedVersion = versions.find(version => version.status === 'approved') || null
  const latestVersionNumber = numberValue(document.latestVersionNumber, latestVersion?.versionNo || 0)
  const latestStatus = stringValue(document.latestStatus || latestVersion?.status || document.status || 'draft').toLowerCase()
  return {
    ...document,
    documentId: stringValue(document.documentId || document.id),
    title: stringValue(document.title || '未命名知识文档'),
    description: stringValue(document.description),
    tags: Array.isArray(document.tags) ? document.tags.map(stringValue).filter(Boolean) : [],
    documentType: stringValue(document.documentType),
    originType: stringValue(document.originType),
    sourceOwnerType: stringValue(document.sourceOwnerType),
    sourceOwnerId: stringValue(document.sourceOwnerId),
    createdByUserId: stringValue(document.createdByUserId),
    createdAt: document.createdAt || '',
    updatedAt: document.updatedAt || '',
    versions,
    latestVersion,
    latestVersionNumber,
    approvedVersion,
    status: latestStatus,
    versionCount: numberValue(document.versionCount, latestVersionNumber || versions.length),
  }
}

export function normalizeKnowledgeDocumentPage(value) {
  const source = objectValue(value)
  const unwrapped = Array.isArray(value) ? {items: value} : objectValue(source.data && !source.items ? source.data : source)
  const items = (Array.isArray(unwrapped.items) ? unwrapped.items : []).map(normalizeKnowledgeDocument)
  return {
    items,
    total: numberValue(unwrapped.total, items.length),
    page: Math.max(1, numberValue(unwrapped.page, 1)),
    pageSize: Math.max(1, numberValue(unwrapped.pageSize, 20)),
  }
}

export function normalizeKnowledgeSearchHits(value) {
  const source = objectValue(value)
  const values = Array.isArray(value) ? value : Array.isArray(source.data) ? source.data : source.items
  return (Array.isArray(values) ? values : []).map((raw, index) => {
    const item = objectValue(raw)
    return {
      ...item,
      retrievalHitId: stringValue(item.retrievalHitId || item.id || `hit-${index + 1}`),
      retrievalRunId: stringValue(item.retrievalRunId),
      documentId: stringValue(item.documentId),
      versionId: stringValue(item.versionId),
      title: stringValue(item.title || '未命名知识文档'),
      versionNo: numberValue(item.versionNo),
      snippet: stringValue(item.snippet || item.excerpt),
      contentSha256: stringValue(item.contentSha256 || item.contentHash || item.sha256),
      sourceOwnerType: stringValue(item.sourceOwnerType),
      sourceOwnerId: stringValue(item.sourceOwnerId),
      status: stringValue(item.status || 'approved').toLowerCase(),
      rank: numberValue(item.rank, index + 1),
      score: numberValue(item.score),
      adopted: item.adopted === true,
      adoptionReason: stringValue(item.adoptionReason),
      verificationStatus: stringValue(item.verificationStatus),
      verificationReason: stringValue(item.verificationReason),
      evidenceSetId: stringValue(item.evidenceSetId),
      evidenceItemId: stringValue(item.evidenceItemId),
    }
  })
}

export function normalizeMemoryCandidate(value = {}) {
  const source = objectValue(value)
  return {
    ...source,
    candidateId: stringValue(source.candidateId || source.id),
    sourceOwnerType: stringValue(source.sourceOwnerType),
    sourceOwnerId: stringValue(source.sourceOwnerId),
    title: stringValue(source.title || '未命名记忆候选'),
    contentText: stringValue(source.contentText || source.content),
    contentSha256: stringValue(source.contentSha256 || source.contentHash || source.sha256),
    status: stringValue(source.status || 'draft').toLowerCase(),
    proposedByActorType: stringValue(source.proposedByActorType),
    proposedByActorId: stringValue(source.proposedByActorId),
    decisionByUserId: stringValue(source.decisionByUserId || source.decidedBy),
    decisionReason: stringValue(source.decisionReason),
    approvedVersionId: stringValue(source.approvedVersionId),
    createdAt: source.createdAt || '',
    decidedAt: source.decidedAt || '',
    updatedAt: source.updatedAt || source.decidedAt || source.createdAt || '',
  }
}

export function normalizeMemoryCandidates(value) {
  const source = objectValue(value)
  const values = Array.isArray(value) ? value : Array.isArray(source.data) ? source.data : source.items
  return (Array.isArray(values) ? values : []).map(normalizeMemoryCandidate)
}

export function knowledgeStatusLabel(status) {
  return {
    draft: '草稿', approved: '已批准', rejected: '已拒绝', superseded: '已替代',
  }[status] || status || '--'
}

export function memoryStatusLabel(status) {
  return status === 'draft' ? '待审批' : knowledgeStatusLabel(status)
}

export function knowledgeStatusType(status) {
  if (status === 'approved') return 'success'
  if (status === 'rejected') return 'error'
  if (status === 'superseded') return 'default'
  if (status === 'draft') return 'warning'
  return 'info'
}

export function knowledgeStatusMeaning(status) {
  return {
    draft: '草稿不会进入研究检索，需用户批准。',
    approved: '已批准且未被替代的版本可进入研究检索；引用时仍必须由当前市场证据复核。',
    rejected: '已拒绝版本不会进入研究检索。',
    superseded: '该版本已被更新版本替代，不再进入研究检索。',
  }[status] || ''
}

export function memoryStatusMeaning(status) {
  if (status === 'draft') return '记忆候选尚未获用户批准，不能进入研究上下文。'
  if (status === 'approved') return '该候选已由用户批准并生成知识版本；研究引用时仍需当前证据复核。'
  if (status === 'rejected') return '该候选已被用户拒绝，不会进入研究上下文。'
  return ''
}

export function knowledgeSourceLabel(ownerType, ownerId) {
  const type = ownerType === 'research2' ? '研究中心2' : ownerType === 'research1' ? '研究中心1' : ownerType || '手工导入'
  return ownerId ? `${type} / ${ownerId}` : type
}

export function knowledgeOriginLabel(document = {}) {
  if (document.sourceOwnerType || document.sourceOwnerId) {
    return knowledgeSourceLabel(document.sourceOwnerType, document.sourceOwnerId)
  }
  return {
    upload: '手工导入',
    research_report: '研究报告',
    memory_candidate: '记忆候选',
  }[document.originType] || document.originType || '手工导入'
}
