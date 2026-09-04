import assert from 'node:assert/strict'
import test from 'node:test'

import {DEFAULT_MARKET_TAB, MARKET_TABS, findMarketTab} from './market-tab-registry.js'

test('market menu uses one stable registry', () => {
  assert.equal(DEFAULT_MARKET_TAB, '市场快讯')
  assert.equal(MARKET_TABS.length, 16)
  assert.equal(new Set(MARKET_TABS.map(tab => tab.key)).size, MARKET_TABS.length)
  assert.equal(new Set(MARKET_TABS.map(tab => tab.name)).size, MARKET_TABS.length)
  assert.ok(MARKET_TABS.every(tab => typeof tab.icon?.render === 'function'))
  assert.deepEqual(
    MARKET_TABS.filter(tab => tab.activeAware).map(tab => tab.name),
    ['市场快讯', '期指多空', '板块资金流向', '概念资金流向', '当前热门', '融资融券'],
  )
  assert.equal(findMarketTab('融资融券')?.key, 'market13-margin')
})
