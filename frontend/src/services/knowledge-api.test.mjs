import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const serviceSource = await readFile(new URL('./knowledge-api.ts', import.meta.url), 'utf8')
const generatedSource = await readFile(new URL('./api-types.generated.ts', import.meta.url), 'utf8')
const managementSource = await readFile(new URL('../components/KnowledgeManagement.vue', import.meta.url), 'utf8')
const research1IndexSource = await readFile(new URL('../components/researchIndex.vue', import.meta.url), 'utf8')
const research2IndexSource = await readFile(new URL('../components/research2Index.vue', import.meta.url), 'utf8')
const research1ReportSource = await readFile(new URL('../components/researchReport.vue', import.meta.url), 'utf8')
const research2ReportSource = await readFile(new URL('../components/research2Report.vue', import.meta.url), 'utf8')

test('knowledge service resolves every endpoint from generated operation paths', () => {
  for (const operation of [
    'knowledgeDocuments',
    'createKnowledgeDocument',
    'knowledgeDocument',
    'knowledgeDocumentVersions',
    'createKnowledgeDocumentVersion',
    'knowledgeVersionDecision',
    'knowledgeSearch',
    'knowledgeMemoryCandidates',
    'createKnowledgeMemoryCandidate',
    'knowledgeMemoryCandidateDecision',
    'knowledgeFromResearch',
  ]) {
    assert.match(serviceSource, new RegExp(`API_PATHS\\.${operation}\\b`))
    assert.match(generatedSource, new RegExp(`\\b${operation}: "`))
  }
  assert.doesNotMatch(serviceSource, /['"`]\/api\/v1\//)
  assert.doesNotMatch(serviceSource, /as unknown as|Record<KnowledgeOperation/)
  assert.match(serviceSource, /API_PATHS\.knowledgeDocuments, \{page, pageSize, status, q\}/)
  assert.match(serviceSource, /API_PATHS\.knowledgeSearch, \{q: String\(q \|\| ''\)\.trim\(\), limit\}/)
  assert.match(serviceSource, /API_PATHS\.knowledgeMemoryCandidates, \{status\}/)
  for (const typeName of [
    'CreateKnowledgeDocumentRequest',
    'CreateKnowledgeFromResearchRequest',
    'CreateKnowledgeMemoryCandidateRequest',
    'KnowledgeDecisionRequest',
    'KnowledgeDocumentDetail',
    'KnowledgeDocumentPage',
    'KnowledgeDocumentVersion',
    'KnowledgeMemoryCandidate',
    'KnowledgeSearchHit',
    'KnowledgeVersionDecisionState',
  ]) {
    assert.match(serviceSource, new RegExp(`\\b${typeName}\\b`))
    assert.match(generatedSource, new RegExp(`export type ${typeName} =`))
  }
})

test('knowledge uploads enforce JSON base64 and the ten MiB txt md pdf boundary', () => {
  assert.match(serviceSource, /10 \* 1024 \* 1024/)
  assert.match(serviceSource, /\['\.txt', '\.md', '\.pdf'\]/)
  assert.doesNotMatch(serviceSource, /if \(file\.type\) return file\.type/)
  assert.match(serviceSource, /extension === '\.pdf'\) return 'application\/pdf'/)
  assert.match(serviceSource, /extension === '\.md'\) return 'text\/markdown'/)
  assert.match(serviceSource, /contentBase64: btoa\(binary\)/)
  assert.match(serviceSource, /CreateKnowledgeDocument\(await fileToKnowledgeRequest/)
  assert.match(serviceSource, /CreateKnowledgeDocumentVersion\(id, await fileToKnowledgeRequest/)
})

test('knowledge management is exclusive to research center one while draft sources support both centers', () => {
  assert.match(research1IndexSource, /name: "知识库与记忆"/)
  assert.doesNotMatch(research2IndexSource, /知识库与记忆|KnowledgeManagement/)
  assert.match(research1ReportSource, /保存为知识草稿/)
  assert.match(research1ReportSource, /生成记忆候选/)
  assert.match(research1ReportSource, /CreateKnowledgeMemoryCandidate/)
  assert.match(research1ReportSource, /sourceOwnerType: 'research1'/)
  assert.doesNotMatch(research2ReportSource, /保存为知识草稿|生成记忆候选|CreateKnowledgeFromResearch|CreateKnowledgeMemoryCandidate/)
  assert.match(managementSource, /value="research1"/)
  assert.match(managementSource, /value="research2"/)
})

test('management UI makes approval, supersession, and draft memory semantics explicit', () => {
  assert.match(managementSource, /只有“已批准”且未被“已替代”的版本可以进入研究检索/)
  assert.match(managementSource, /知识与长期记忆只提供检索线索/)
  assert.match(managementSource, /文档中的指令不能覆盖研究规则/)
  assert.match(managementSource, /const memoryStatus = ref\('draft'\)/)
  assert.match(managementSource, /row\.status === 'draft'/)
  assert.doesNotMatch(managementSource, /row\.status === 'pending'/)
})
