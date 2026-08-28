import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const serviceSource = await readFile(new URL('./research-audit-api.ts', import.meta.url), 'utf8')
const panelSource = await readFile(new URL('../components/research-audit/ResearchAuditPanel.vue', import.meta.url), 'utf8')
const research1Source = await readFile(new URL('../components/researchReport.vue', import.meta.url), 'utf8')
const research2Source = await readFile(new URL('../components/research2Report.vue', import.meta.url), 'utf8')

test('audit service resolves only generated operation paths', () => {
  for (const operation of [
    'getResearchAnalysisRunAudit',
    'exportResearchAnalysisRunAudit',
    'getResearch2AnalysisRunAudit',
    'exportResearch2AnalysisRunAudit',
    'createResearchReplay',
    'getResearchReplay',
  ]) {
    assert.match(serviceSource, new RegExp(`API_PATHS\\.${operation}\\b`))
  }
  assert.doesNotMatch(serviceSource, /['"`]\/api\/v1\//)
  assert.match(serviceSource, /CreateResearchReplayRequest\s*=\s*\{sourceOwnerType, sourceOwnerId, modelConfigId\}/)
  assert.match(serviceSource, /API_PATHS\.createResearchReplay,\s*\{method:\s*'POST', body\}/)
  assert.match(serviceSource, /response\.blob\(\)/)
})

test('both report details use one shared five-tab audit panel', () => {
  assert.match(research1Source, /ResearchAuditPanel owner-type="research1"/)
  assert.match(research2Source, /ResearchAuditPanel owner-type="research2"/)
  for (const label of ['最终结果', '提示词与输入', '证据快照', '模型调用', '原始响应与修复']) {
    assert.match(panelSource, new RegExp(`tab="${label}"`))
  }
  assert.match(panelSource, /legacy_unavailable/)
  assert.match(panelSource, /不会改写正式推荐、交易、持仓或账户/)
  assert.match(panelSource, /audit\.value\.state\.status !== 'capturing'/)
  assert.match(panelSource, /setTimeout\(refreshAudit, 2000\)/)
  assert.match(panelSource, /setTimeout\(refreshReplay, 2000\)/)
  assert.match(panelSource, /onBeforeUnmount\([\s\S]*replayRequestVersion\+\+[\s\S]*stopAuditPolling\(\)[\s\S]*stopReplayPolling\(\)/)
  assert.match(panelSource, /requestVersion !== replayRequestVersion/)
})
