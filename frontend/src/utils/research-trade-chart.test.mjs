import assert from 'node:assert/strict'
import test from 'node:test'

import {
  readPriceLinesPreference,
  RESEARCH_CHART_PRICE_LINES_STORAGE_KEY,
  tradingDaySeparatorIndexes,
  weightedExecutionPrice,
  writePriceLinesPreference,
} from './research-trade-chart.js'

test('adds one separator before the first bar of each later trading day', () => {
  const separators = tradingDaySeparatorIndexes([
    '2026-08-17T09:30:00+08:00',
    '2026-08-17T11:30:00+08:00',
    '2026-08-17T13:00:00+08:00',
    '2026-08-18T09:30:00+08:00',
    '2026-08-18T15:00:00+08:00',
    '2026-08-20T09:30:00+08:00',
  ])

  assert.deepEqual([...separators], [3, 5])
})

test('does not add separators for one day or invalid empty categories', () => {
  assert.deepEqual([...tradingDaySeparatorIndexes([
    '2026-08-21T09:30:00+08:00',
    '2026-08-21T13:00:00+08:00',
    '',
  ])], [])
})

test('price lines default to hidden and persist one global browser preference', () => {
  const values = new Map()
  const storage = {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  }

  assert.equal(readPriceLinesPreference(storage), false)
  writePriceLinesPreference(storage, true)
  assert.equal(values.get(RESEARCH_CHART_PRICE_LINES_STORAGE_KEY), 'true')
  assert.equal(readPriceLinesPreference(storage), true)
  writePriceLinesPreference(storage, false)
  assert.equal(readPriceLinesPreference(storage), false)
})

test('invalid or unavailable browser storage falls back to hidden', () => {
  assert.equal(readPriceLinesPreference({getItem: () => 'invalid'}), false)
  assert.equal(readPriceLinesPreference({getItem: () => { throw new Error('blocked') }}), false)
  assert.doesNotThrow(() => writePriceLinesPreference({setItem: () => { throw new Error('blocked') }}, true))
})

test('calculates quantity-weighted buy and sell execution prices', () => {
  const trades = [
    {side: 'buy', executionPrice: 10, quantity: 100},
    {side: 'BUY', executionPrice: 12, quantity: 300},
    {side: 'sell', executionPrice: 13, quantity: 200},
    {side: 'sell', executionPrice: 14, quantity: 200},
    {side: 'sell', executionPrice: 0, quantity: 10},
  ]

  assert.equal(weightedExecutionPrice(trades, 'buy'), 11.5)
  assert.equal(weightedExecutionPrice(trades, 'sell'), 13.5)
  assert.equal(weightedExecutionPrice(trades, 'unknown'), 0)
})
