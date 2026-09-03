import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const report = await readFile(new URL('./research2Report.vue', import.meta.url), 'utf8')
const recommendations = await readFile(new URL('./research2Recommendations.vue', import.meta.url), 'utf8')
const yieldView = await readFile(new URL('./research2Yield.vue', import.meta.url), 'utf8')
const settings = await readFile(new URL('./settings.vue', import.meta.url), 'utf8')
const generated = await readFile(new URL('../services/api-types.generated.ts', import.meta.url), 'utf8')

test('research2 report exposes trailing-five evidence timing, attempt and quality', () => {
  for (const token of ['attemptNo', 'scheduledFor', 'startedAt', 'evidenceWindowStartAt', 'evidenceCutoffAt', 'generatedAt', 'evidenceCoveragePct', 'degraded']) {
    assert.match(report, new RegExp(token))
    assert.match(generated, new RegExp(token))
  }
  assert.match(report, /实际启动前5个已闭合交易分钟/)
  assert.match(report, /分析批次/)
  assert.doesNotMatch(report, /09:55 冻结证据/)
  assert.match(settings, /实际启动前5个已闭合交易分钟/)
  assert.doesNotMatch(settings, /09:55 冻结数据/)
  assert.doesNotMatch(settings, /11:30 前完成/)
  assert.doesNotMatch(report, /11:25|11:30/)
  for (const source of [report, recommendations, settings]) {
    assert.match(source, /\[09:50,13:00\)/)
    assert.match(source, /13:00 前生成/)
    assert.match(source, /13:00 起/)
    assert.doesNotMatch(source, /11:25|11:30/)
  }
})

test('research2 details expose target and actual execution provenance', () => {
  for (const source of [recommendations, yieldView]) {
    assert.match(source, /targetBuyAt/)
    assert.match(source, /buyAt/)
    assert.match(source, /targetSellAt/)
    assert.match(source, /sellAt/)
    assert.match(source, /priceSource/)
    assert.match(source, /recovered_target_minute/)
  }
  assert.match(generated, /executionMode\?: "live_after_signal" \| "recovered_target_minute"/)
  assert.match(generated, /priceSource\?: string/)
})

test('research2 analysis-only recommendations are explicit and non-transactional', () => {
  assert.match(recommendations, /analysis_only: '仅分析'/)
  assert.match(recommendations, /status === 'analysis_only' \? 'default'/)
  assert.match(recommendations, /不会进入模拟买卖或收益统计/)
  assert.match(recommendations, /不会创建模拟成交或计入策略收益/)
  assert.match(recommendations, /13:00 起生成的“仅分析”推荐/)
  assert.match(yieldView, /analysis_only: '仅分析'/)
  assert.match(generated, /\| "analysis_only"/)
})
