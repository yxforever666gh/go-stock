import assert from 'node:assert/strict'
import test from 'node:test'
import {dataDisplayStatus} from './data-status.js'

test('empty, unknown and failed responses never claim complete data', () => {
  assert.notEqual(dataDisplayStatus({}).type, 'success')
  assert.equal(dataDisplayStatus({}, {loading: true}).label, '加载中')
  assert.equal(dataDisplayStatus({}, {error: 'offline'}).label, '加载失败')
  assert.notEqual(dataDisplayStatus({status: 'unexpected', data: [1]}).type, 'success')
  assert.notEqual(dataDisplayStatus({status: 'ok', data: []}).type, 'success')
})

test('chart coverage is distinct from refresh failure and provider fallback diagnostics', () => {
  const chart = {status: 'ok', bars: [{close: 1}], fetchedAt: '2026-09-07T11:00:00+08:00'}
  assert.equal(dataDisplayStatus(chart, {chart: true}).label, '分钟覆盖完整')
  assert.equal(dataDisplayStatus({...chart, errors: [{message: 'primary failed; fallback used'}]}, {chart: true}).label, '分钟覆盖完整')
  assert.equal(dataDisplayStatus(chart, {chart: true, error: 'refresh failed'}).label, '使用上次数据')
  assert.equal(dataDisplayStatus({...chart, missingIntervals: [{}]}, {chart: true}).label, '部分分钟数据')
  assert.equal(dataDisplayStatus({status: 'partial', bars: []}, {chart: true}).label, '暂无分钟数据')
})

test('explicit partial, stale and cutoff states remain visible', () => {
  assert.equal(dataDisplayStatus({status: 'partial', data: [1]}).label, '部分数据')
  assert.equal(dataDisplayStatus({status: 'stale', data: [1]}).label, '已过期')
  assert.equal(dataDisplayStatus({status: 'after_cutoff', data: [1]}).label, '截止后数据')
  assert.equal(dataDisplayStatus({status: 'ok', data: [1]}).label, '数据可用')
})
