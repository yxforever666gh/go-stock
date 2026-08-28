import assert from 'node:assert/strict'
import test from 'node:test'

import {
  AUDIT_TABS,
  auditIsAvailable,
  auditIsLegacy,
  modelConfigOptions,
  normalizeResearchAudit,
  prettyAuditValue,
  replayDifference,
  replayIsTerminal,
  replayStatus,
} from './audit-model.js'

test('normalizes available and legacy audit contracts without inventing payloads', () => {
  const available = normalizeResearchAudit({
    availability: 'available', ownerType: 'research1', ownerId: 'run-1',
    state: {status: 'completed', payloadCount: 1},
    payloads: [{phase: 'market', finalPrompt: 'prompt', redactionCount: 2}],
  })
  assert.equal(auditIsAvailable(available), true)
  assert.equal(available.payloads[0].payloadId, 'payload-1')
  assert.equal(available.payloads[0].callSequence, 1)
  assert.equal(available.payloads[0].redactionCount, 2)

  const legacy = normalizeResearchAudit({data: {availability: 'legacy_unavailable'}}, {ownerType: 'research2', ownerId: 'old-run'})
  assert.equal(auditIsLegacy(legacy), true)
  assert.equal(legacy.ownerId, 'old-run')
  assert.deepEqual(legacy.payloads, [])
})

test('keeps the five required audit tabs in stable order', () => {
  assert.deepEqual(AUDIT_TABS.map(item => item.label), [
    '最终结果', '提示词与输入', '证据快照', '模型调用', '原始响应与修复',
  ])
})

test('normalizes replay state and readonly difference payloads', () => {
  assert.equal(replayStatus({state: {status: 'COMPLETED'}}), 'completed')
  assert.equal(replayIsTerminal({status: 'completed'}), true)
  assert.equal(replayIsTerminal({status: 'failed'}), true)
  assert.equal(replayIsTerminal({status: 'running'}), false)
  assert.deepEqual(replayDifference({diffSummary: {changed: 2}, result: 'new report'}), {changed: 2})
  assert.equal(prettyAuditValue('{"safe":true}'), '{\n  "safe": true\n}')
})

test('only offers enabled persisted model configurations', () => {
  assert.deepEqual(modelConfigOptions([
    {ID: 2, name: '主模型', modelName: 'gpt-x'},
    {id: 3, name: '备用', modelName: 'model-y', disabled: true},
    {ID: 0, name: '未保存'},
  ]), [{value: 2, label: '主模型（gpt-x）'}])
})
