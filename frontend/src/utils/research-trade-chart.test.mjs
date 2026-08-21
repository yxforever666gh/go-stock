import assert from 'node:assert/strict'
import test from 'node:test'

import {tradingDaySeparatorIndexes} from './research-trade-chart.js'

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
