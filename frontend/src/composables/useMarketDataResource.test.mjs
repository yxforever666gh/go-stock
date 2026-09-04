import assert from 'node:assert/strict'
import test from 'node:test'
import {nextTick, ref} from 'vue'

import {hasUsableEnvelopeData, useMarketDataResource} from './useMarketDataResource.js'

test('only successful envelopes with meaningful data can replace a snapshot', () => {
  assert.equal(hasUsableEnvelopeData({status: 'ok', data: {advanceCount: 12}}), true)
  assert.equal(hasUsableEnvelopeData({status: 'partial', data: {rows: [{code: 'BK1'}]}}), true)
  assert.equal(hasUsableEnvelopeData({status: 'stale', data: [1]}), true)
  assert.equal(hasUsableEnvelopeData({status: 'ok', data: {snapshots: [{time: '09:25:00'}]}}), true)
  assert.equal(hasUsableEnvelopeData({status: 'unavailable', data: {rows: [{code: 'BK1'}]}}), false)
  assert.equal(hasUsableEnvelopeData({status: 'after_cutoff', data: []}), false)
  assert.equal(hasUsableEnvelopeData({status: 'ok', data: {rows: []}}), false)
})

async function flushReactivity() {
  await Promise.resolve()
  await nextTick()
  await Promise.resolve()
}

test('request identity changes clear old success data and reject superseded responses', async () => {
  const active = ref(true)
  const requestKey = ref('IF|')
  const requests = []
  const resource = useMarketDataResource({
    active,
    fallbackData: {rows: []},
    intervalMs: 60 * 60 * 1000,
    requestKey,
    session: 'always',
    loader: () => new Promise(resolve => requests.push(resolve)),
  })

  assert.equal(requests.length, 1)
  requestKey.value = 'IM|'
  await flushReactivity()
  assert.equal(requests.length, 2)
  assert.deepEqual(resource.data.value, {rows: []})

  requests[0]({status: 'ok', data: {rows: [{symbol: 'IF'}]}})
  await flushReactivity()
  assert.deepEqual(resource.data.value, {rows: []})

  requests[1]({status: 'unavailable', data: {rows: []}, errors: [{message: 'IM unavailable'}]})
  await flushReactivity()
  assert.deepEqual(resource.data.value, {rows: []})
  assert.equal(resource.envelope.value.status, 'unavailable')
  assert.equal(resource.error.value, 'IM unavailable')
  resource.dispose()
})

test('a new request identity resets an already successful snapshot before loading', async () => {
  const active = ref(true)
  const requestKey = ref('market||')
  const requests = []
  const resource = useMarketDataResource({
    active,
    fallbackData: {rows: []},
    intervalMs: 60 * 60 * 1000,
    requestKey,
    session: 'always',
    loader: () => new Promise(resolve => requests.push(resolve)),
  })

  requests[0]({status: 'ok', data: {rows: [{scope: 'market'}]}})
  await flushReactivity()
  assert.deepEqual(resource.data.value, {rows: [{scope: 'market'}]})

  requestKey.value = 'security|sh600000|'
  await flushReactivity()
  assert.deepEqual(resource.data.value, {rows: []})
  assert.equal(resource.envelope.value.status, 'unavailable')
  resource.dispose()
})
