import assert from 'node:assert/strict'
import test from 'node:test'
import {
  compareOptional,
  formatFlowAmount,
  formatFlowPercent,
  fundFlowTone,
  fundFlowTradingDate,
  limitedFundFlowSelection,
  normalizeFundFlowRows,
} from './fund-flow-model.js'

test('fund flow rows preserve missing values instead of inventing zeroes', () => {
  const [row] = normalizeFundFlowRows([{code: 'BK0001', name: '半导体', netAmount: 123456789, changePct: null}])
  assert.equal(row._netInflow, 123456789)
  assert.equal(row._changePercent, null)
  assert.equal(row._largeNetAmount, null)
  assert.equal(formatFlowAmount(row._largeNetAmount), '—')
  assert.equal(formatFlowPercent(row._changePercent), '—')
})

test('fund flow presentation uses red-up green-down and neutral zero without a plus sign', () => {
  assert.equal(formatFlowAmount(123456789), '+1.23 亿')
  assert.equal(formatFlowAmount(-12345), '-1.23 万')
  assert.equal(formatFlowPercent(1.5), '+1.50%')
  assert.equal(formatFlowPercent(0), '0.00%')
  assert.equal(fundFlowTone(1), 'error')
  assert.equal(fundFlowTone(-1), 'success')
  assert.equal(fundFlowTone(0), 'default')
})

test('fund flow date prefers the actual timeline session over fetch time', () => {
  assert.equal(fundFlowTradingDate({asOf: '2026-09-05T12:00:00+08:00'}, [{data: {tradingDate: '2026-09-04'}}]), '2026-09-04')
  assert.equal(fundFlowTradingDate({asOf: '2026-09-04T15:00:00+08:00'}), '2026-09-04')
  assert.equal(fundFlowTradingDate({asOf: '0001-01-01T00:00:00Z'}), '—')
})

test('fund flow chart selection is capped at six rows', () => {
  assert.deepEqual(limitedFundFlowSelection(['1', '2', '3', '4', '5', '6', '7']), ['1', '2', '3', '4', '5', '6'])
  assert.deepEqual(limitedFundFlowSelection(null), [])
})

test('missing values sort separately from zero and negative amounts', () => {
  assert.equal(compareOptional(null, -1), -1)
  assert.equal(compareOptional(0, null), 1)
  assert.equal(compareOptional(null, undefined), 0)
})
