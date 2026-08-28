import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const serviceSource = await readFile(new URL('./market-api.js', import.meta.url), 'utf8')
const pathsSource = await readFile(new URL('./api-types.generated.ts', import.meta.url), 'utf8')

test('market evidence services only use canonical API_PATHS', () => {
  const operations = [
    ['getMarketBreadth', '/api/v1/market/breadth'],
    ['listMarketFundFlows', '/api/v1/market/fund-flows'],
    ['listFuturesPositions', '/api/v1/market/futures/positions'],
    ['getMarginTrading', '/api/v1/market/margin'],
    ['getInstrumentAuction', '/api/v1/instruments/{code}/auction'],
    ['listInstrumentTrades', '/api/v1/instruments/{code}/trades'],
  ]
  for (const [operation, path] of operations) {
    assert.match(serviceSource, new RegExp(`API_PATHS\\.${operation}\\b`))
    assert.equal(pathsSource.includes(`${operation}: \"${path}\"`), true)
  }
  assert.equal(serviceSource.includes('/api/v1/'), false)
  assert.match(serviceSource, /requestDataEnvelope/)
})
