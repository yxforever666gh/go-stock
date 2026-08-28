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

test('both research settings expose a default-off experimental evidence switch and persist it', () => {
  assert.match(settingsSource, /experimentalEvidenceEnabled:\s*false/)
  assert.match(settingsSource, /config\?\.experimentalEvidenceEnabled === true/)
  assert.match(settingsSource, /experimentalEvidenceEnabled:\s*formValue\.value\.experimentalEvidenceEnabled === true/)
  assert.match(settingsSource, /path="experimentalEvidenceEnabled"/)
  assert.match(settingsSource, /默认关闭；开启后两套研究会接入实验市场证据并可能改变研究结果，市场行情页面不受影响/)
  assert.doesNotMatch(settingsSource, /v-if="settingsScope === '[^']+'"[^\n]*path="experimentalEvidenceEnabled"/)
})
