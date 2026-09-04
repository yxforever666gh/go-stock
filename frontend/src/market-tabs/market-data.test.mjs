import assert from 'node:assert/strict'
import test from 'node:test'

import {
  auctionSummaryFrom,
  formatOptionalMetric,
  historyFrom,
  itemCode,
  itemName,
  latestDatedRows,
  normalizeFuturesPositionRows,
  numberValue,
  optionalNumberValue,
  rowsFrom,
} from './market-data.js'

test('normalizes market row collections and provider field aliases', () => {
  const row = {sectorCode: 'BK001', sectorName: '半导体', net_inflow: '12.5', trend: [{time: '10:00'}]}
  assert.deepEqual(rowsFrom({items: [row]}), [row])
  assert.deepEqual(rowsFrom({snapshots: [row]}), [row])
  assert.equal(itemCode(row), 'BK001')
  assert.equal(itemName(row), '半导体')
  assert.equal(numberValue(row, ['netInflow', 'net_inflow']), 12.5)
  assert.deepEqual(historyFrom(row), [{time: '10:00'}])
})

test('normalizes futures rows as a stable trading-day series', () => {
  const rows = normalizeFuturesPositionRows({rows: [
    {tradeDate: '2026-08-28', settlePrice: 4012.4, longPosition: 12, shortPosition: 9, netPosition: 3, indexClose: 3998, basis: 14.4},
    {tradeDate: '2026-08-27', longPosition: 10, shortPosition: 8, netPosition: 2},
  ]})
  assert.deepEqual(rows.map(row => row._date), ['2026-08-27', '2026-08-28'])
  assert.equal(rows[1]._settlePrice, 4012.4)
  assert.equal(rows[1]._net, 3)
  assert.equal(rows[1]._basis, 14.4)
})

test('selects only the newest dated rows for snapshot summaries', () => {
  const rows = [{_date: '2026-08-27', code: 'old'}, {_date: '2026-08-28', code: 'sse'}, {_date: '2026-08-28', code: 'szse'}]
  assert.deepEqual(latestDatedRows(rows).map(row => row.code), ['sse', 'szse'])
  assert.deepEqual(latestDatedRows(rows, {single: true}).map(row => row.code), ['sse'])
})

test('keeps nullable evidence distinct from a numeric zero', () => {
  assert.equal(optionalNumberValue({unmatchedVolume: null}, ['unmatchedVolume']), null)
  assert.equal(optionalNumberValue({unmatchedVolume: 0}, ['unmatchedVolume']), 0)
  assert.equal(formatOptionalMetric({newHighs: null}, ['newHighs']), '—')
  assert.equal(formatOptionalMetric({gapPct: 1.25}, ['gapPct'], {digits: 2, signed: true, suffix: '%'}), '+1.25%')
})

test('uses the explicit final auction snapshot before summaries and row fallbacks', () => {
  const finalSnapshot = {time: '09:25:00', price: 10.2}
  const data = {finalSnapshot, summary: {price: 10.1}, snapshots: [{time: '09:24:59', price: 10.0}]}
  assert.equal(auctionSummaryFrom(data), finalSnapshot)
  assert.deepEqual(auctionSummaryFrom({snapshots: [{time: '09:25:00'}]}), {time: '09:25:00'})
})
