import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const serviceSource = await readFile(new URL('./instruments-api.js', import.meta.url), 'utf8')
const pathsSource = await readFile(new URL('./api-types.generated.ts', import.meta.url), 'utf8')

test('instrument chart and drawing services only resolve generated API_PATHS', () => {
  for (const operation of ['getInstrumentChart', 'getInstrumentDrawings', 'putInstrumentDrawings', 'deleteInstrumentDrawings']) {
    assert.match(pathsSource, new RegExp(`${operation}: "/api/v1/instruments/\\{code\\}/(?:chart|drawings)"`))
  }
  assert.match(serviceSource, /API_PATHS\[operation\]/)
  assert.equal(serviceSource.includes('/api/v1/'), false)
  assert.match(serviceSource, /assetType, market, period, adjustment, from, to, limit/)
  assert.match(serviceSource, /expectedRevision/)
  assert.match(serviceSource, /method: 'DELETE'/)
  assert.match(serviceSource, /API_PATHS\.getStockKLine/)
  assert.match(serviceSource, /legacy:getStockKLine/)
})
