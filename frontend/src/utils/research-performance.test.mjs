import assert from 'node:assert/strict'
import test from 'node:test'
import {
  formatHoldingMinutes,
  normalizeAccountOverview,
  normalizeCashFlows,
  normalizePerformance,
  sampleAssessment,
} from './research-performance.js'

test('sample assessment follows the 30 and 100 closed trade thresholds', () => {
  assert.equal(sampleAssessment(29).label, '样本不足')
  assert.equal(sampleAssessment(30).label, '初步观察')
  assert.equal(sampleAssessment(99).label, '初步观察')
  assert.equal(sampleAssessment(100).label, '可进行阶段性评价')
})

test('account overview derives fixed-capital return without funding or capacity fields', () => {
  const result = normalizeAccountOverview({
    initialCash: 500000,
    cumulativeNetContribution: 500000,
    currentPositions: 6,
    pendingBuys: 1,
    netProfit: 25000,
    netYieldRate: 0.08,
  })
  assert.equal(result.cumulativeCapitalReturn, 0.05)
  assert.equal(result.timeWeightedReturn, 0.08)
  assert.equal(result.currentPositions, 6)
  assert.equal(result.pendingBuys, 1)
})

test('performance and cash flow payloads degrade safely when optional metrics are absent', () => {
  const result = normalizePerformance({metrics: {closedTrades: 12}}, {netProfit: 200, netAssetValue: 100200})
  assert.equal(result.metrics.sampleAssessment, '样本不足')
  assert.equal(result.metrics.winRate, null)
  assert.equal(result.netProfit, 200)
  assert.deepEqual(normalizeCashFlows({items: [{amount: '100000'}]}).map(item => item.amount), [100000])
  assert.deepEqual(normalizeCashFlows(null), [])
})

test('performance normalization follows the account performance API fields', () => {
  const result = normalizePerformance({metrics: {
    closedTrades: 35,
    sampleLevel: '初步观察',
    averageGainRate: 0.08,
    averageLossRate: -0.03,
    averageHoldingMinutes: 390,
  }})
  assert.equal(result.metrics.sampleAssessment, '初步观察')
  assert.equal(result.metrics.sampleAssessmentType, 'info')
  assert.equal(result.metrics.averageGainRate, 0.08)
  assert.equal(result.metrics.averageLossRate, -0.03)
  assert.equal(result.metrics.averageHoldingMinutes, 390)
})

test('holding duration remains readable for minutes, hours and days', () => {
  assert.equal(formatHoldingMinutes(null), '--')
  assert.equal(formatHoldingMinutes(270), '4.5 小时')
  assert.equal(formatHoldingMinutes(2970), '2 天 1.5 小时')
})
