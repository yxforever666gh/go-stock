import assert from 'node:assert/strict'
import test from 'node:test'
import {
  MEMORY_CANDIDATE_STATUSES,
  MEMORY_STATUS_OPTIONS,
  knowledgeStatusMeaning,
  memoryStatusLabel,
  normalizeKnowledgeDocument,
  normalizeKnowledgeSearchHits,
  normalizeMemoryCandidate,
} from './knowledge-model.js'

test('knowledge document preserves immutable version states and sorts newest first', () => {
  const document = normalizeKnowledgeDocument({
    documentId: 'doc-1',
    title: '复盘方法',
    versions: [
      {versionId: 'v1', versionNo: 1, status: 'superseded', contentSha256: 'old'},
      {versionId: 'v3', versionNo: 3, status: 'draft', contentSha256: 'draft'},
      {versionId: 'v2', versionNo: 2, status: 'approved', contentSha256: 'live'},
    ],
  })

  assert.deepEqual(document.versions.map(version => version.versionId), ['v3', 'v2', 'v1'])
  assert.equal(document.status, 'draft')
  assert.equal(document.approvedVersion.versionId, 'v2')
  assert.equal(document.versions[2].status, 'superseded')
  assert.match(knowledgeStatusMeaning('approved'), /未被替代/)
  assert.match(knowledgeStatusMeaning('approved'), /当前市场证据复核/)
  assert.match(knowledgeStatusMeaning('superseded'), /不再进入研究检索/)
})

test('public document summaries retain latest version and origin metadata', () => {
  const document = normalizeKnowledgeDocument({
    documentId: 'doc-public',
    title: '公开摘要',
    documentType: 'research_report',
    originType: 'research_report',
    sourceOwnerType: 'research2',
    sourceOwnerId: 'run-2',
    latestVersionNumber: 4,
    latestStatus: 'approved',
    latestSourceFilename: 'report.md',
    latestContentSha256: 'abcdef',
  })

  assert.equal(document.latestVersionNumber, 4)
  assert.equal(document.status, 'approved')
  assert.equal(document.latestVersion.sourceFilename, 'report.md')
  assert.equal(document.latestVersion.contentSha256, 'abcdef')
  assert.equal(document.documentType, 'research_report')
  assert.equal(document.sourceOwnerType, 'research2')
  assert.equal(document.sourceOwnerId, 'run-2')
})

test('memory candidates use draft as the only pending-approval state', () => {
  const candidate = normalizeMemoryCandidate({candidateId: 'memory-1'})

  assert.deepEqual(MEMORY_CANDIDATE_STATUSES, ['draft', 'approved', 'rejected'])
  assert.equal(candidate.status, 'draft')
  assert.equal(memoryStatusLabel(candidate.status), '待审批')
  assert.equal(MEMORY_STATUS_OPTIONS.find(option => option.value === 'draft')?.label, '待审批')
  assert.equal(MEMORY_STATUS_OPTIONS.some(option => option.value === 'pending'), false)
})

test('search normalization supports public excerpt and source fields plus optional retrieval audit fields', () => {
  const [hit] = normalizeKnowledgeSearchHits([{ 
    retrievalHitId: 'hit-1',
    retrievalRunId: 'run-1',
    documentId: 'doc-1',
    versionId: 'v2',
    title: '复盘方法',
    versionNo: 2,
    excerpt: '严格止损',
    contentSha256: 'hash-1',
    sourceOwnerType: 'research1',
    sourceOwnerId: 'run-1',
    status: 'approved',
    rank: 1,
    score: 0.88,
    adopted: true,
    adoptionReason: '与当前问题相关',
    verificationStatus: 'verified',
    verificationReason: '本次市场证据已复核',
    evidenceSetId: 'set-1',
    evidenceItemId: 'item-1',
  }])

  assert.equal(hit.adopted, true)
  assert.equal(hit.snippet, '严格止损')
  assert.equal(hit.contentSha256, 'hash-1')
  assert.equal(hit.sourceOwnerType, 'research1')
  assert.equal(hit.status, 'approved')
  assert.equal(hit.adoptionReason, '与当前问题相关')
  assert.equal(hit.verificationStatus, 'verified')
  assert.equal(hit.verificationReason, '本次市场证据已复核')
  assert.equal(hit.evidenceSetId, 'set-1')
  assert.equal(hit.evidenceItemId, 'item-1')
})
