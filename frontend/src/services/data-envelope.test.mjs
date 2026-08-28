import assert from 'node:assert/strict'
import test from 'node:test'

import {markEnvelopeStale, parseDataEnvelope} from './data-envelope.js'

test('normalizes current and legacy data payloads', () => {
  assert.deepEqual(parseDataEnvelope([1, 2]).data, [1, 2])
  const result = parseDataEnvelope({
    data: {rows: [1]},
    source: ['primary', 'fallback'],
    as_of: '2026-08-28T10:00:00+08:00',
    fetched_at: '2026-08-28T10:00:01+08:00',
    status: 'partial',
    errors: ['fallback timeout'],
    sources: [{provider: 'primary', status: 'partial'}],
    warnings: ['fallback degraded'],
    evidenceProfile: 'market-evidence-v1',
    evidenceSetId: 'evidence-1',
  })
  assert.deepEqual(result.data, {rows: [1]})
  assert.equal(result.partial, true)
  assert.equal(result.status, 'partial')
  assert.equal(result.asOf, '2026-08-28T10:00:00+08:00')
  assert.deepEqual(result.sources, [{provider: 'primary', status: 'partial'}])
  assert.deepEqual(result.warnings, ['fallback degraded'])
  assert.equal(result.evidenceProfile, 'market-evidence-v1')
  assert.equal(result.evidenceSetId, 'evidence-1')
  assert.equal(parseDataEnvelope({data: []}).status, 'ok')
  assert.equal(parseDataEnvelope({data: [], status: 'unavailable', errors: ['down']}).status, 'unavailable')
  const unavailable = parseDataEnvelope({
    data: [], status: 'unavailable', asOf: '0001-01-01T00:00:00Z', fetchedAt: '2026-08-28T10:00:01+08:00',
  })
  assert.equal(unavailable.asOf, '')
  assert.equal(unavailable.fetchedAt, '2026-08-28T10:00:01+08:00')
})

test('retains the last successful data when a refresh becomes stale', () => {
  const previous = parseDataEnvelope({data: {rows: [{code: 'BK1'}]}, source: 'primary'})
  const stale = markEnvelopeStale(previous, new Error('network unavailable'))
  assert.deepEqual(stale.data, previous.data)
  assert.equal(stale.stale, true)
  assert.equal(stale.status, 'stale')
  assert.deepEqual(stale.errors, ['network unavailable'])
})
