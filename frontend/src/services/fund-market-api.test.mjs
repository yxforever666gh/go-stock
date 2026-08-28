import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const serviceSource = await readFile(new URL('./fund-market-api.ts', import.meta.url), 'utf8')
const generatedSource = await readFile(new URL('./api-types.generated.ts', import.meta.url), 'utf8')
const fundViewSource = await readFile(new URL('../views/FundView.vue', import.meta.url), 'utf8')
const rankingsSource = await readFile(new URL('../components/funds/FundRankingsPane.vue', import.meta.url), 'utf8')
const fundListSource = await readFile(new URL('../components/funds/FundRankingList.vue', import.meta.url), 'utf8')
const etfListSource = await readFile(new URL('../components/funds/ETFRankingList.vue', import.meta.url), 'utf8')

test('fund and ETF services resolve only the seven generated operation paths', () => {
  for (const operation of [
    'fundRankings', 'etfRankings', 'etfSearch', 'etfDetail',
    'listFollowedETFs', 'followETF', 'unfollowETF',
  ]) {
    assert.match(serviceSource, new RegExp(`API_PATHS\\.${operation}\\b`))
    assert.match(generatedSource, new RegExp(`\\b${operation}: "`))
  }
  assert.doesNotMatch(serviceSource, /['"`]\/api\/v1\//)
  assert.doesNotMatch(serviceSource, /FUND_MARKET_OPERATIONS|FundMarketPaths|as unknown as/)
  assert.match(serviceSource, /withQuery\(API_PATHS\.fundRankings, query\)/)
  assert.match(serviceSource, /withQuery\(API_PATHS\.etfRankings, query\)/)
  assert.match(serviceSource, /withQuery\(API_PATHS\.etfSearch, \{q: String\(q \|\| ''\)\.trim\(\), limit\}\)/)
  assert.match(serviceSource, /withPath\(API_PATHS\.etfDetail, \{code\}\)/)
  assert.match(serviceSource, /command\(API_PATHS\.followETF/)
  assert.match(serviceSource, /withPath\(API_PATHS\.unfollowETF, \{code\}\)/)
  for (const typeName of [
    'ETFDetailEnvelope', 'ETFRankingEnvelope', 'ETFRankingPage', 'ETFSearchEnvelope',
    'ETFWatchlistItem', 'ETFWatchlistRequest', 'FundRankingEnvelope', 'FundRankingPage',
  ]) {
    assert.match(serviceSource, new RegExp(`\\b${typeName}\\b`))
    assert.match(generatedSource, new RegExp(`export type ${typeName} =`))
  }
  assert.match(generatedSource, /export type ETFWatchlistRequest = \{\s*category:/)
  assert.doesNotMatch(serviceSource, /FollowFund|UnFollowFund|listFollowedFunds/)
})

test('fund page keeps two async top-level panes and separate off-exchange and ETF rankings', () => {
  assert.match(fundViewSource, /defineAsyncComponent\(\(\) => import\('\.\.\/components\/fund\.vue'\)\)/)
  assert.match(fundViewSource, /defineAsyncComponent\(\(\) => import\('\.\.\/components\/funds\/FundRankingsPane\.vue'\)\)/)
  assert.match(fundViewSource, /tab="基金自选"/)
  assert.match(fundViewSource, /tab="基金排行"/)
  assert.match(rankingsSource, /tab="场外基金排行"/)
  assert.match(rankingsSource, /tab="场内 ETF"/)
  assert.match(fundViewSource, /delete query\.etfCode/)
  assert.match(rankingsSource, /delete query\.etfCode/)
})

test('rankings keep all query dimensions in server request identities and expose source dates', () => {
  for (const token of ['category.value', 'period.value', 'query.value', 'sortDirection.value', 'page.value', 'pageSize.value']) {
    assert.match(fundListSource, new RegExp(token.replace('.', '\\.')))
  }
  for (const token of ['category.value', 'query.value', 'sort.value', 'sortDirection.value', 'page.value', 'pageSize.value']) {
    assert.match(etfListSource, new RegExp(token.replace('.', '\\.')))
  }
  assert.match(fundListSource, /EvidenceStatusBar/)
  assert.match(fundListSource, /rankingPage\.navDate/)
  assert.match(etfListSource, /EvidenceStatusBar/)
  assert.match(etfListSource, /quoteTime/)
})

test('ETF detail reuses the unified ETF chart and offers information and watchlist actions only', () => {
  assert.match(etfListSource, /<KLineChart/)
  assert.match(etfListSource, /asset-type="etf"/)
  assert.match(etfListSource, /adjustment="none"/)
  assert.match(etfListSource, /跟踪指数/)
  assert.match(etfListSource, /管理费/)
  assert.match(etfListSource, /主要持仓/)
  assert.match(etfListSource, /加入 ETF 自选/)
  assert.match(etfListSource, /ETF 自选清单/)
  assert.match(etfListSource, /ListFollowedETFs/)
  assert.match(etfListSource, /category: row\.category/)
  assert.doesNotMatch(etfListSource, /category: row\.category \|\| undefined/)
  assert.doesNotMatch(etfListSource, /模拟交易|创建交易|买入|卖出|荐股/)
})
