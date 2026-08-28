import assert from 'node:assert/strict'
import test from 'node:test'
import {
  CHART_PERIODS,
  chartModelFromEnvelope,
  defaultChartAdjustment,
  drawingScopeKey,
  isLegacyChartInstrument,
  normalizeAdjustment,
  normalizeChartBars,
  normalizeInstrument,
} from './chart-contract.js'

test('chart contract covers all periods and keeps canonical uppercase market', () => {
  assert.deepEqual(CHART_PERIODS.map(item => item.value), ['1m', '5m', '15m', '30m', '60m', 'day', 'week', 'month', 'quarter', 'year'])
  assert.deepEqual(normalizeInstrument({assetType: 'ETF', market: 'sh', code: '510300'}), {assetType: 'etf', market: 'SH', code: '510300'})
  assert.equal(drawingScopeKey({instrument: {assetType: 'etf', market: 'sh', code: '510300'}, period: '5m', adjustment: 'qfq'}), 'etf|SH|510300|5m|qfq')
})

test('HK and US instruments are explicitly routed to the legacy data adapter', () => {
  assert.equal(isLegacyChartInstrument({market: 'HK', code: 'hkHSI'}), true)
  assert.equal(isLegacyChartInstrument({market: 'US', code: 'us.IXIC'}), true)
  assert.equal(isLegacyChartInstrument({code: 'gb_aapl'}), true)
  assert.equal(isLegacyChartInstrument({market: 'SH', code: 'sh000001'}), false)
})

test('stock and ETF accept all adjustments while defaults and index remain safe', () => {
  assert.equal(defaultChartAdjustment('stock'), 'qfq')
  assert.equal(defaultChartAdjustment('etf'), 'none')
  assert.equal(normalizeAdjustment('hfq', 'stock'), 'hfq')
  assert.equal(normalizeAdjustment('qfq', 'etf'), 'qfq')
  assert.equal(normalizeAdjustment('', 'etf'), 'none')
  assert.equal(normalizeAdjustment('hfq', 'index'), 'none')
})

test('bars are validated, sorted and deduplicated by canonical time', () => {
  const bars = normalizeChartBars([
    {time: '2026-01-02', open: 2, close: 3, low: 1, high: 4},
    {time: '2026-01-01', open: 1, close: 1.5, low: 0.5, high: 2},
    {time: '2026-01-02', open: 20, close: 30, low: 10, high: 40, source: 'latest'},
    {time: 'bad', open: 1, close: 1, low: 5, high: 2},
    {time: '', open: 1, close: 1, low: 1, high: 1},
  ])
  assert.equal(bars.length, 2)
  assert.equal(bars[0].time, '2026-01-01')
  assert.equal(bars[1].close, 30)
  assert.equal(bars[1].source, 'latest')
})

test('envelope preserves status, source times and missing intervals', () => {
  const model = chartModelFromEnvelope({
    data: {
      instrument: {assetType: 'stock', market: 'sz', code: '000001'},
      bars: [{time: '2026-01-01', open: 1, close: 1, low: 1, high: 1}],
      missingIntervals: [{from: '2026-01-01T10:00:00', to: '2026-01-01T10:05:00', reason: 'provider_gap'}],
    },
    status: 'partial', source: 'tencent', asOf: 'a', fetchedAt: 'f', errors: ['timeout'],
  })
  assert.equal(model.instrument.market, 'SZ')
  assert.equal(model.status, 'partial')
  assert.equal(model.missingIntervals[0].reason, 'provider_gap')
  assert.equal(model.errors[0], 'timeout')
})
