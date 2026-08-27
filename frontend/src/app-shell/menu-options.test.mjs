import test from 'node:test'
import assert from 'node:assert/strict'
import {researchCenterMenuModel} from './research-menu-model.js'

test('research centers are split and settings are shared tabs', () => {
  const centers = researchCenterMenuModel()
  assert.deepEqual(centers.map(item => item.key), ['research', 'research2'])
  assert.deepEqual(centers.map(item => item.label), ['研究中心1', '研究中心2'])
  assert.equal(centers.every(item => item.tabs.at(-1) === '设置'), true)
  assert.equal(centers.every(item => item.tabs.length === 4), true)
  assert.deepEqual(centers.map(item => item.settingsScope), ['research1', 'research2'])
})
