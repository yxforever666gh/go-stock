import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const settingsSource = await readFile(new URL('./settings.vue', import.meta.url), 'utf8')
const reportSource = await readFile(new URL('./research2Report.vue', import.meta.url), 'utf8')

test('Research Center 2 owns its report email settings and test action', () => {
  assert.match(settingsSource, /v-if="settingsScope === 'research2'"[\s\S]{0,300}研究中心2报告邮件/)
  assert.match(settingsSource, /research2EmailEnabled/)
  assert.match(settingsSource, /TestResearch2Email/)
  assert.doesNotMatch(settingsSource, /settingsScope === 'research1'[^\n]*研究中心2报告邮件/)
})

test('Research Center 2 report page exposes persisted delivery state', () => {
  assert.match(reportSource, /emailDeliveryStatus/)
  assert.match(reportSource, /emailAttemptCount/)
  assert.match(reportSource, /emailLastError/)
})
