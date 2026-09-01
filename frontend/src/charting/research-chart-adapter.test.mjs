import assert from 'node:assert/strict'
import test from 'node:test'
import {adaptResearchChart, researchChartOverlays} from './research-chart-adapter.js'

const chart = {
  stockCode: '600000', stockName: '浦发银行', status: 'partial', refreshedAt: '2026-01-02T15:00:00Z',
  currentPrice: 12, currentNetPnl: 100, currentNetYieldRate: 0.01,
  missingSessions: ['2026-01-03'], providerErrors: [{provider: 'sina', message: 'timeout'}],
  sessions: [{date: '2026-01-02', previousClose: 10, status: 'partial'}, {date: '2026-01-03', previousClose: 11, status: 'missing'}],
  bars: [
    {at: '2026-01-02T09:31:00+08:00', open: 10, close: 11, low: 9, high: 12, volume: 1, amount: 10, source: 'tencent', netPnl: 5, netYieldRate: 0.005},
    {at: '2026-01-02T09:32:00+08:00', open: 11, close: 12, low: 10, high: 13, volume: 2, amount: 20, source: 'tencent', netPnl: 10, netYieldRate: 0.01},
  ],
}
const trades = [{side: 'buy', markerAt: chart.bars[0].at, tradedAt: chart.bars[0].at, executionPrice: 10, quantity: 100, totalFees: 1, markerSnapped: false}]

test('research adapter preserves real bars, source errors, PnL and missing sessions', () => {
  const model = adaptResearchChart(chart)
  assert.equal(model.status, 'partial')
  assert.equal(model.bars[0].raw.netPnl, 5)
  assert.equal(model.missingIntervals[0].reason, '交易日分钟数据缺失')
  assert.equal(model.errors[0].provider, 'sina')
  const overlays = researchChartOverlays(model, trades, {showPriceLines: true})
  assert.ok(overlays.mainMarkPoints.some(item => item.value === 'B'))
  assert.ok(overlays.mainMarkLines.some(item => item.name === '买入均价'))
  assert.match(overlays.tooltipLines(model.bars[0]).join(' '), /预估净收益/)
})

test('partial session is not expanded into a false full-day missing interval', () => {
  const current = {
    stockCode: '600551', stockName: '时代出版', status: 'partial',
    rangeFrom: '2026-09-01T09:30:00+08:00', rangeTo: '2026-09-01T11:19:00+08:00',
    missingSessions: ['2026-09-01'],
    sessions: [{date: '2026-09-01', previousClose: 0, status: 'partial'}],
    bars: [{at: '2026-09-01T11:15:00+08:00', open: 9, close: 9.08, low: 9, high: 9.08, source: 'tencent'}],
  }

  const model = adaptResearchChart(current)
  assert.equal(model.missingIntervals.length, 0)
  assert.match(researchChartOverlays(model).tooltipLines(model.bars[0])[0], /涨跌幅：--/)
})

test('missing current session interval is clamped to the chart range', () => {
  const current = {
    stockCode: '600551', status: 'empty',
    rangeFrom: '2026-09-01T09:30:00+08:00', rangeTo: '2026-09-01T11:19:00+08:00',
    missingSessions: ['2026-09-01'],
    sessions: [{date: '2026-09-01', previousClose: 8.25, status: 'missing'}],
    bars: [],
  }

  const model = adaptResearchChart(current)
  assert.equal(model.missingIntervals.length, 1)
  assert.equal(model.missingIntervals[0].from, '2026-09-01T01:30:00.000Z')
  assert.equal(model.missingIntervals[0].to, '2026-09-01T03:19:00.000Z')
})
