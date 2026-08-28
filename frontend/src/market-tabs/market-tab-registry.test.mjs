import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

import {DEFAULT_MARKET_TAB, MARKET_TABS, findMarketTab} from './market-tab-registry.js'

const hotTopicsSource = await readFile(new URL('./HotTopicsTab.vue', import.meta.url), 'utf8')
const dailyThemesSource = await readFile(new URL('./DailyThemesPane.vue', import.meta.url), 'utf8')
const detailSource = await readFile(new URL('../components/themes/ThemeDetailDrawer.vue', import.meta.url), 'utf8')

test('market menu and page use one stable registry', () => {
  assert.equal(DEFAULT_MARKET_TAB, '市场快讯')
  assert.equal(MARKET_TABS.length, 16)
  assert.equal(new Set(MARKET_TABS.map(tab => tab.key)).size, MARKET_TABS.length)
  assert.equal(new Set(MARKET_TABS.map(tab => tab.name)).size, MARKET_TABS.length)
  assert.deepEqual(
    MARKET_TABS.filter(tab => tab.activeAware).map(tab => tab.name),
    ['市场快讯', '期指多空', '板块资金流向', '概念资金流向', '当前热门', '融资融券'],
  )
  assert.equal(findMarketTab('融资融券')?.key, 'market13-margin')
})

test('current hot keeps one top-level menu and restores its two inner views from query', () => {
  assert.equal(MARKET_TABS.filter(tab => tab.name === '当前热门').length, 1)
  assert.match(hotTopicsSource, /name="current" tab="当前热门"/)
  assert.match(hotTopicsSource, /name="daily-themes" tab="每日炒作题材"/)
  for (const key of ['hotView', 'themeId', 'date']) assert.match(hotTopicsSource, new RegExp(`route\\.query\\.${key}`))
  assert.match(hotTopicsSource, /router\.replace/)
})

test('daily theme resources reset by date, stage and theme while historical sessions run outside trading hours', () => {
  assert.match(dailyThemesSource, /requestKey[\s\S]{0,180}props\.date[\s\S]{0,100}stage\.value/)
  assert.match(dailyThemesSource, /session: 'always'/)
  assert.match(dailyThemesSource, /active,/)
  assert.match(detailSource, /detailKey[\s\S]{0,160}props\.themeId[\s\S]{0,100}props\.date/)
  assert.match(detailSource, /historyKey[\s\S]{0,180}props\.themeId/)
  assert.match(detailSource, /catalystsKey[\s\S]{0,180}props\.themeId/)
  assert.equal((detailSource.match(/session: 'always'/g) || []).length, 3)
})

test('theme detail keeps conflicting source claims side by side with event and availability times', () => {
  assert.match(detailSource, /stanceLabel\(claim\.stance\)/)
  assert.match(detailSource, /event\.eventAt/)
  assert.match(detailSource, /event\.firstAvailableAt/)
  assert.match(detailSource, /claim\.availableAt/)
  assert.match(detailSource, /sourceCredibilityScore/)
  assert.match(detailSource, /safeSourceRef/)
})
