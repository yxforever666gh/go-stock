import assert from 'node:assert/strict'
import test from 'node:test'
import {acceptSavedModelIDs, importResearchSettings, researchPayload} from './research-settings.js'

test('research save uses only server-owned fields and keeps the current revision', () => {
  const snapshot = {revision: 3, config: {tushareToken: 'saved', research2AutoEnabled: true}}
  const payload = researchPayload(snapshot, {tushareToken: 'new', darkTheme: true, aiCapitalDeploymentEnabled: false}, [])
  assert.deepEqual(payload, {revision: 3, config: {tushareToken: 'new', research2AutoEnabled: true}, aiConfigs: []})
})

test('save acknowledgments preserve edits, added rows, removal and new order', () => {
  const first = {ID: 10, name: 'first', sort: 1}
  const newRow = {name: 'new', sort: 2}
  const removed = {ID: 20, name: 'removed', sort: 3}
  const submittedRows = [first, newRow, removed]
  const submittedModels = structuredClone(submittedRows)
  first.name = 'edited while saving'
  newRow.apiKey = 'newer-key'
  const newer = {name: 'added while saving'}
  const current = [newRow, newer, first]
  acceptSavedModelIDs(current, submittedRows, submittedModels, [{ID: 10}, {ID: 21}, {ID: 20}])
  assert.deepEqual(current.map(row => row.ID), [21, undefined, 10])
  assert.deepEqual(current.map(row => row.sort), [1, 2, 3])
  assert.equal(current[2].name, 'edited while saving')
  assert.equal(current[0].apiKey, 'newer-key')
  assert.equal(removed.sort, 3)
})

test('imports strip model IDs and global fields without adopting exported revision', () => {
  const current = {revision: 8, config: {tushareToken: 'saved', research2AutoEnabled: true}, aiConfigs: []}
  const imported = importResearchSettings(current, {revision: 1, darkTheme: false, tushareToken: 'imported', aiConfigs: [{ID: 33, CreatedAt: 'old', apiKey: 'key'}]}, 'research2')
  assert.equal(imported.revision, 8)
  assert.deepEqual(imported.config, {tushareToken: 'imported', research2AutoEnabled: true})
  assert.deepEqual(imported.aiConfigs, [{ID: 0, apiKey: 'key'}])
  assert.throws(() => importResearchSettings(current, {center: 'research1', config: {}}, 'research2'), /当前研究中心/)
})
