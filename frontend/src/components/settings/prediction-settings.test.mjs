import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'
import {acceptSavedModelIDs, importPredictionSettings, predictionPayload} from './prediction-settings.js'

test('research save uses only server-owned fields and keeps the current revision', () => {
  const snapshot = {revision: 3, config: {tushareToken: 'saved', predictionAutoEnabled: true}}
  const payload = predictionPayload(snapshot, {tushareToken: 'new', darkTheme: true, aiCapitalDeploymentEnabled: false}, [])
  assert.deepEqual(payload, {revision: 3, config: {tushareToken: 'new', predictionAutoEnabled: true}, aiConfigs: []})
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
  const current = {revision: 8, config: {tushareToken: 'saved', predictionAutoEnabled: true}, aiConfigs: []}
  const imported = importPredictionSettings(current, {revision: 1, darkTheme: false, tushareToken: 'imported', aiConfigs: [{ID: 33, CreatedAt: 'old', apiKey: 'key'}]})
  assert.equal(imported.revision, 8)
  assert.deepEqual(imported.config, {tushareToken: 'imported', predictionAutoEnabled: true})
  assert.deepEqual(imported.aiConfigs, [{ID: 0, apiKey: 'key'}])
  assert.throws(() => importPredictionSettings(current, {center: 'research1', config: {}}), /股票预测/)
})

test('prediction email slot selection round-trips through save and import payloads', () => {
  const snapshot = {revision: 4, config: {predictionEmailEnabled: false, predictionEmailSlots: []}, aiConfigs: []}
  const saved = predictionPayload(snapshot, {predictionEmailEnabled: true, predictionEmailSlots: ['09:30', '10:00']}, [])
  assert.deepEqual(saved.config.predictionEmailSlots, ['09:30', '10:00'])
  const imported = importPredictionSettings(snapshot, {center: 'prediction', config: {predictionEmailSlots: ['11:25']}})
  assert.deepEqual(imported.config.predictionEmailSlots, ['11:25'])
})

test('prediction email settings render the shared searchable multi-select and draft-only SMTP test', async () => {
  const source = await readFile(new URL('../settings.vue', import.meta.url), 'utf8')
  assert.match(source, /PREDICTION_SLOTS/)
  assert.match(source, /v-model:value="formValue\.predictionEmail\.slots" multiple filterable clearable/)
  assert.match(source, /getPredictionEmailConfigError\(true, false\)/)
  assert.match(source, /TestPredictionEmail\(\{[\s\S]*smtpPassword: email\.smtpPassword/)
  assert.doesNotMatch(source, /if \(!await saveCurrentConfig[\s\S]{0,100}TestPredictionEmail/)
})

test('pre-upgrade prediction exports retain auto and email values without accepting the removed center', () => {
  const snapshot = {revision: 5, config: {predictionAutoEnabled: false, predictionEmailSlots: []}, aiConfigs: []}
  const result = importPredictionSettings(snapshot, {center: 'research2', config: {research2AutoEnabled: true, research2EmailSlots: ['10:05']}})
  assert.deepEqual(result.config, {predictionAutoEnabled: true, predictionEmailSlots: ['10:05']})
  assert.equal(result.revision, 5)
})
