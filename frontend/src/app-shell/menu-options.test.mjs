import test from 'node:test'
import assert from 'node:assert/strict'
import {createMemoryHistory, createRouter} from 'vue-router'
import {navigation} from './navigation.js'
import {routes} from '../router/routes.js'

test('Stock God exposes prediction, settings and about as its complete navigation', () => {
  assert.deepEqual(navigation, [
    {key: 'prediction', label: '股票预测'},
    {key: 'settings', label: '设置'},
    {key: 'about', label: '关于'},
  ])
  const router = createRouter({history: createMemoryHistory(), routes})
  for (const {key} of navigation) {
    assert.equal(router.resolve({name: key}).path, `/${key}`)
    assert.equal(router.resolve(`/${key}`).name, key)
  }
  assert.deepEqual(router.resolve('/').matched[0].redirect, {name: 'prediction'})
  for (const removed of ['/market', '/research', '/research2', '/fund', '/stock', '/knowledge']) {
    assert.deepEqual(router.resolve(removed).matched[0].redirect, {name: 'prediction'})
  }
})
