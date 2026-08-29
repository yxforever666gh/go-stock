import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const serviceSource = await readFile(new URL('./research-api.ts', import.meta.url), 'utf8')
const generatedSource = await readFile(new URL('./api-types.generated.ts', import.meta.url), 'utf8')
const reportSource = await readFile(new URL('../components/researchReport.vue', import.meta.url), 'utf8')
const settingsSource = await readFile(new URL('../components/settings.vue', import.meta.url), 'utf8')

test('research one exposes event-driven capital deployment reads and no manual analysis action', () => {
  assert.match(serviceSource, /GetAICapitalDeploymentStatus/)
  assert.match(serviceSource, /ListAIBuyOpportunities/)
  assert.match(generatedSource, /getCapitalDeploymentStatus: "\/api\/v1\/research\/capital-deployment\/status"/)
  assert.match(generatedSource, /listBuyOpportunities: "\/api\/v1\/research\/buy-opportunities"/)
  assert.doesNotMatch(generatedSource, /startAnalysisRun/)
  assert.doesNotMatch(serviceSource, /StartAIAnalysis/)
  assert.doesNotMatch(reportSource, /开始 AI 分析|StartAIAnalysis|startAnalysis/)
})

test('settings persist the capital deployment policy and retain independent holding reviews', () => {
  for (const field of [
    'aiCapitalDeploymentEnabled',
    'aiTargetCapitalUtilization',
    'aiMaxImmediateBuysPerRun',
    'aiReanalysisIntervalMinutes',
    'aiReviewStartTime',
    'aiReviewIntervalMinutes',
  ]) assert.match(settingsSource, new RegExp(field))
  assert.match(settingsSource, /资金补位策略/)
  assert.doesNotMatch(settingsSource, /自动分析时间|aiAnalysisTimes/)
})

test('analysis reports identify trigger context and all three opportunity actions', () => {
  assert.match(reportSource, /triggerSource/)
  assert.match(reportSource, /triggerReason/)
  assert.match(reportSource, /buyNowCount/)
  assert.match(reportSource, /waitCount/)
  assert.match(reportSource, /rejectCount/)
  assert.match(reportSource, /历史定时运行/)
  assert.match(reportSource, /buy_now:\s*'立即买入'/)
  assert.match(reportSource, /wait:\s*'等待'/)
  assert.match(reportSource, /reject:\s*'放弃'/)
})
