import assert from 'node:assert/strict'
import test from 'node:test'
import {
  ETF_CATEGORIES,
  ETF_SORT_OPTIONS,
  FUND_CATEGORIES,
  FUND_PERIODS,
  etfIdentity,
  fundPeriodMetric,
  normalizeETFDetail,
  normalizeETFRankingPage,
  normalizeFundRankingPage,
} from './fund-market-model.js'

test('fund ranking enums and period metrics match the public contract', () => {
  assert.deepEqual(FUND_CATEGORIES.map(item => item.value), ['all', 'stock', 'mixed', 'bond', 'index', 'qdii', 'fof'])
  assert.deepEqual(FUND_PERIODS.map(item => item.value), ['day', 'week', 'month', '3m', '6m', '1y', '3y', 'ytd', 'since_inception', 'scale'])

  const page = normalizeFundRankingPage({
    items: [{code: '000001', name: '测试基金', dayReturn: 0, threeMonthReturn: 3.2, scale: 100000000}],
    total: 1, page: 1, pageSize: 20, category: 'all', period: 'day', navDate: '2026-08-28',
  })
  assert.equal(fundPeriodMetric(page.items[0], 'day'), 0)
  assert.equal(fundPeriodMetric(page.items[0], '3m'), 3.2)
  assert.equal(fundPeriodMetric(page.items[0], 'scale'), 100000000)
  assert.equal(page.navDate, '2026-08-28')
})

test('ETF ranking keeps nullable market data and canonical uppercase market', () => {
  assert.deepEqual(ETF_CATEGORIES.map(item => item.value), ['all', 'broad', 'industry', 'cross_border', 'bond', 'commodity', 'money'])
  assert.deepEqual(ETF_SORT_OPTIONS.map(item => item.value), ['changeRate', 'amount', 'turnoverRate', 'premiumRate', 'scale', 'netInflow'])

  const page = normalizeETFRankingPage({items: [{code: '510300', name: '沪深300ETF', market: 'sh', changeRate: 0, nav: null}]})
  assert.equal(page.items[0].market, 'SH')
  assert.equal(page.items[0].changeRate, 0)
  assert.equal(page.items[0].nav, null)
  assert.equal(etfIdentity(page.items[0]), 'SH:510300')
})

test('ETF detail reuses its chart instrument and sorts holdings without inventing values', () => {
  const detail = normalizeETFDetail({
    code: '159915', name: '创业板ETF', market: 'sz', trackingIndex: '创业板指', managementFee: 0.5,
    chartInstrument: {assetType: 'etf', market: 'sz', code: '159915'},
    holdings: [
      {code: '300002', name: '乙', weight: null, asOf: '2026-06-30'},
      {code: '300001', name: '甲', weight: 8.5, asOf: '2026-06-30'},
    ],
  })
  assert.deepEqual(detail.chartInstrument, {assetType: 'etf', market: 'SZ', code: '159915'})
  assert.equal(detail.holdings[0].code, '300001')
  assert.equal(detail.holdings[1].weight, null)
  assert.equal(detail.trackingIndex, '创业板指')
})
