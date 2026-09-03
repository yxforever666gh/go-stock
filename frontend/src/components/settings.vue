<script setup>
import {h, onBeforeUnmount, onMounted, ref} from 'vue'
import {NTag, useMessage} from 'naive-ui'
import {GetConfig, TestAIConfig, TestResearch2Email, UpdateConfig} from '../services/settings-api'
import {ExportConfig} from '../services/exports-api'
import {EventsEmit} from '../services/browser-runtime.mjs'
import MinuteProviderSettings from './settings/MinuteProviderSettings.vue'
import AiConfigSettings from './settings/AiConfigSettings.vue'

const props = defineProps({
  settingsScope: {
    type: String,
    default: 'research1',
    validator: value => ['research1', 'research2'].includes(value),
  },
})

const message = useMessage()
const formRef = ref(null)
const formValue = ref({
  ID: 1,
  darkTheme: true,
  enableFund: false,
  tushareToken: '',
  qgqpBId: '',
  updateBasicInfoOnStart: false,
  refreshInterval: 1,
  minuteLongHistoryHintEnabled: true,
  minuteProviderOrder: ['tencent', 'sina', 'akshare', 'private'],
  akshareEnabled: true,
  sinaMinuteEnabled: true,
  tencentMinuteEnabled: true,
  akshareMinuteSourceMode: 'auto',
  privateMinute: {
    enabled: false,
    baseUrl: '',
    apiKey: '',
    timeoutSec: 60,
    minIntervalMs: 1200,
    proxyMode: 'disable',
    level: '1min',
  },
  openAI: {aiConfigs: []},
  capitalDeployment: {
    enabled: true,
    targetCapitalUtilization: 90,
    maxImmediateBuysPerRun: 2,
    reanalysisIntervalMinutes: 30,
    reviewStartTime: '09:50',
    reviewIntervalMinutes: 15,
  },
  experimentalEvidenceEnabled: false,
  research2AutoEnabled: true,
  research2Email: {
    enabled: false,
    to: '',
    from: '',
    smtpHost: '',
    smtpPort: 465,
    smtpUsername: '',
    smtpPassword: '',
  },
})

const aiConfigTestStates = ref({})
const settingsLoaded = ref(false)
const persistedConfig = ref({})
const autoSaveState = ref('idle')
const autoSaveError = ref('')
const autoSaveLastSavedAt = ref('')
const research2EmailTesting = ref(false)
let activeSavePromise = null
let queuedAutoSave = false
let nextAiConfigLocalKey = 1
const aiConfigLocalKeys = new WeakMap()

const akshareMinuteSourceOptions = [
  {label: '自动', value: 'auto'},
  {label: '新浪', value: 'sina'},
  {label: '东方财富', value: 'em'},
]
const privateMinuteProxyModeOptions = [
  {label: '强制直连', value: 'disable'},
  {label: '跟随系统代理', value: 'inherit'},
]
const privateMinuteLevelOptions = [
  {label: '1 分钟', value: '1min'},
  {label: '5 分钟', value: '5min'},
  {label: '15 分钟', value: '15min'},
  {label: '30 分钟', value: '30min'},
  {label: '60 分钟', value: '60min'},
]
const aiProtocolOptions = [
  {label: 'Chat Completions', value: 'chat_completions'},
  {label: 'OpenAI Responses', value: 'openai_responses'},
  {label: 'Anthropic Messages', value: 'anthropic_messages'},
]

function normalizeAiProtocol(value) {
  return ['openai_responses', 'anthropic_messages'].includes(String(value || '').trim())
    ? String(value).trim()
    : 'chat_completions'
}

function normalizeAiConfigs(configs) {
  return (configs || []).map((item, index) => ({
    ...item,
    sort: index + 1,
    disabled: item?.disabled === true,
    apiProtocol: normalizeAiProtocol(item?.apiProtocol),
  }))
}

function normalizeProviderOrder(order) {
  const valid = ['tencent', 'sina', 'akshare', 'private']
  const normalized = []
  for (const provider of Array.isArray(order) ? order : []) {
    if (valid.includes(provider) && !normalized.includes(provider)) normalized.push(provider)
  }
  for (const provider of valid) {
    if (!normalized.includes(provider)) normalized.push(provider)
  }
  return normalized
}

function primaryAiConfigId(configs = formValue.value.openAI.aiConfigs) {
  return (configs || []).find(item => item?.disabled !== true)?.ID || 0
}

function applyConfigToForm(config) {
  const aiConfigs = normalizeAiConfigs(config?.aiConfigs || [])
  persistedConfig.value = {...(config || {}), aiConfigs}
  formValue.value.ID = config?.ID || 1
  formValue.value.darkTheme = config?.darkTheme === true
  formValue.value.enableFund = config?.enableFund === true
  formValue.value.tushareToken = config?.tushareToken || ''
  formValue.value.qgqpBId = config?.qgqpBId || ''
  formValue.value.updateBasicInfoOnStart = config?.updateBasicInfoOnStart === true
  formValue.value.refreshInterval = Number.isFinite(config?.refreshInterval) ? config.refreshInterval : 1
  formValue.value.minuteLongHistoryHintEnabled = config?.minuteLongHistoryHintEnabled !== false
  formValue.value.minuteProviderOrder = normalizeProviderOrder(config?.minuteProviderOrder)
  formValue.value.akshareEnabled = config?.akshareEnabled !== false
  formValue.value.sinaMinuteEnabled = config?.sinaMinuteEnabled !== false
  formValue.value.tencentMinuteEnabled = config?.tencentMinuteEnabled !== false
  formValue.value.akshareMinuteSourceMode = config?.akshareMinuteSourceMode || 'auto'
  formValue.value.privateMinute = {
    enabled: config?.privateMinuteEnabled === true,
    baseUrl: config?.privateMinuteBaseUrl || '',
    apiKey: config?.privateMinuteApiKey || '',
    timeoutSec: config?.privateMinuteTimeoutSec || 60,
    minIntervalMs: Number.isFinite(config?.privateMinuteMinIntervalMs) ? config.privateMinuteMinIntervalMs : 1200,
    proxyMode: config?.privateMinuteProxyMode || 'disable',
    level: config?.privateMinuteLevel || '1min',
  }
  formValue.value.openAI.aiConfigs = aiConfigs
  formValue.value.capitalDeployment = {
    enabled: config?.aiCapitalDeploymentEnabled !== false,
    targetCapitalUtilization: Math.round((Number.isFinite(config?.aiTargetCapitalUtilization) ? config.aiTargetCapitalUtilization : 0.9) * 100),
    maxImmediateBuysPerRun: Number.isFinite(config?.aiMaxImmediateBuysPerRun) ? config.aiMaxImmediateBuysPerRun : 2,
    reanalysisIntervalMinutes: Number.isFinite(config?.aiReanalysisIntervalMinutes) ? config.aiReanalysisIntervalMinutes : 30,
    reviewStartTime: config?.aiReviewStartTime || '09:50',
    reviewIntervalMinutes: config?.aiReviewIntervalMinutes || 15,
  }
  formValue.value.experimentalEvidenceEnabled = config?.experimentalEvidenceEnabled === true
  formValue.value.research2AutoEnabled = config?.research2AutoEnabled !== false
  formValue.value.research2Email = {
    enabled: config?.research2EmailEnabled === true,
    to: config?.research2EmailTo || '',
    from: config?.research2EmailFrom || '',
    smtpHost: config?.research2EmailSmtpHost || '',
    smtpPort: Number.isFinite(config?.research2EmailSmtpPort) && config.research2EmailSmtpPort > 0 ? config.research2EmailSmtpPort : 465,
    smtpUsername: config?.research2EmailSmtpUsername || '',
    smtpPassword: config?.research2EmailSmtpPassword || '',
  }
}

function aiConfigRowKey(aiConfig) {
  if (aiConfig?.ID) return `id-${aiConfig.ID}`
  if (!aiConfig || typeof aiConfig !== 'object') return `new-${nextAiConfigLocalKey++}`
  if (!aiConfigLocalKeys.has(aiConfig)) aiConfigLocalKeys.set(aiConfig, `new-${nextAiConfigLocalKey++}`)
  return aiConfigLocalKeys.get(aiConfig)
}

function renumberAiConfigSorts() {
  formValue.value.openAI.aiConfigs.forEach((item, index) => {
    item.sort = index + 1
    item.apiProtocol = normalizeAiProtocol(item.apiProtocol)
  })
}

function addAiConfig() {
  formValue.value.openAI.aiConfigs.push({
    sort: formValue.value.openAI.aiConfigs.length + 1,
    disabled: false,
    name: '',
    baseUrl: 'https://api.deepseek.com',
    apiKey: '',
    modelName: 'deepseek-chat',
    apiProtocol: 'chat_completions',
    temperature: 0.1,
    maxTokens: 4096,
    timeOut: 300,
    httpProxy: '',
    httpProxyEnabled: false,
  })
  queueAutoSave()
}

function removeAiConfig(index) {
  formValue.value.openAI.aiConfigs.splice(index, 1)
  renumberAiConfigSorts()
  queueAutoSave()
}

function moveItem(items, sourceIndex, targetIndex) {
  if (!Number.isInteger(sourceIndex) || !Number.isInteger(targetIndex) || sourceIndex === targetIndex ||
      sourceIndex < 0 || targetIndex < 0 || sourceIndex >= items.length || targetIndex >= items.length) return false
  const [moved] = items.splice(sourceIndex, 1)
  items.splice(targetIndex, 0, moved)
  return true
}

function handleAiConfigMove(sourceIndex, targetIndex) {
  if (moveItem(formValue.value.openAI.aiConfigs, sourceIndex, targetIndex)) {
    renumberAiConfigSorts()
    queueAutoSave()
  }
}

function handleProviderMove(sourceIndex, targetIndex) {
  if (moveItem(formValue.value.minuteProviderOrder, sourceIndex, targetIndex)) queueAutoSave()
}

function aiConfigTestKey(aiConfig, index) {
  return aiConfigRowKey(aiConfig) || `new-${index}`
}

function aiConfigTestState(aiConfig, index) {
  return aiConfigTestStates.value[aiConfigTestKey(aiConfig, index)] || {}
}

async function testAiConfig(index) {
  const current = formValue.value.openAI.aiConfigs[index]
  const key = aiConfigTestKey(current, index)
  aiConfigTestStates.value = {...aiConfigTestStates.value, [key]: {loading: true, result: null}}
  try {
    if (!await saveCurrentConfig({notifyError: true})) return
    const latest = await GetConfig()
    formValue.value.openAI.aiConfigs = normalizeAiConfigs(latest.aiConfigs || [])
    const savedConfig = formValue.value.openAI.aiConfigs[index]
    const savedKey = aiConfigTestKey(savedConfig, index)
    if (!savedConfig?.ID) throw new Error('请先保存 AI 配置后再测试')
    const result = await TestAIConfig(Number(savedConfig.ID))
    aiConfigTestStates.value = {...aiConfigTestStates.value, [key]: {loading: false}, [savedKey]: {loading: false, result}}
    result?.success ? message.success(`模型测试成功：${result.contentPreview || result.message}`) : message.error(result?.message || '模型测试失败')
  } catch (error) {
    aiConfigTestStates.value = {...aiConfigTestStates.value, [key]: {loading: false, result: {success: false, message: error?.message || String(error)}}}
    message.error(error?.message || String(error || '模型测试失败'))
  }
}

function buildConfigPayload() {
  renumberAiConfigSorts()
  return {
    ...persistedConfig.value,
    ID: formValue.value.ID,
    darkTheme: formValue.value.darkTheme,
    enableFund: formValue.value.enableFund,
    tushareToken: formValue.value.tushareToken,
    qgqpBId: formValue.value.qgqpBId,
    updateBasicInfoOnStart: formValue.value.updateBasicInfoOnStart,
    refreshInterval: formValue.value.refreshInterval,
    minuteLongHistoryHintEnabled: formValue.value.minuteLongHistoryHintEnabled,
    minuteProviderOrder: formValue.value.minuteProviderOrder,
    akshareEnabled: formValue.value.akshareEnabled,
    sinaMinuteEnabled: formValue.value.sinaMinuteEnabled,
    tencentMinuteEnabled: formValue.value.tencentMinuteEnabled,
    akshareMinuteSourceMode: formValue.value.akshareMinuteSourceMode,
    privateMinuteEnabled: formValue.value.privateMinute.enabled,
    privateMinuteBaseUrl: formValue.value.privateMinute.baseUrl,
    privateMinuteApiKey: formValue.value.privateMinute.apiKey,
    privateMinuteTimeoutSec: formValue.value.privateMinute.timeoutSec,
    privateMinuteMinIntervalMs: formValue.value.privateMinute.minIntervalMs,
    privateMinuteProxyMode: formValue.value.privateMinute.proxyMode,
    privateMinuteLevel: formValue.value.privateMinute.level,
    aiConfigs: formValue.value.openAI.aiConfigs,
    aiCapitalDeploymentEnabled: formValue.value.capitalDeployment.enabled,
    aiTargetCapitalUtilization: formValue.value.capitalDeployment.targetCapitalUtilization / 100,
    aiMaxImmediateBuysPerRun: formValue.value.capitalDeployment.maxImmediateBuysPerRun,
    aiReanalysisIntervalMinutes: formValue.value.capitalDeployment.reanalysisIntervalMinutes,
    aiAnalysisConfigId: primaryAiConfigId(),
    aiReviewStartTime: formValue.value.capitalDeployment.reviewStartTime,
    aiReviewIntervalMinutes: formValue.value.capitalDeployment.reviewIntervalMinutes,
    experimentalEvidenceEnabled: formValue.value.experimentalEvidenceEnabled === true,
    research2AutoEnabled: formValue.value.research2AutoEnabled,
    research2EmailEnabled: formValue.value.research2Email.enabled,
    research2EmailTo: formValue.value.research2Email.to,
    research2EmailFrom: formValue.value.research2Email.from,
    research2EmailSmtpHost: formValue.value.research2Email.smtpHost,
    research2EmailSmtpPort: formValue.value.research2Email.smtpPort,
    research2EmailSmtpUsername: formValue.value.research2Email.smtpUsername,
    research2EmailSmtpPassword: formValue.value.research2Email.smtpPassword,
  }
}

function getResearch2EmailConfigError(requireConfig = formValue.value.research2Email.enabled) {
  if (props.settingsScope !== 'research2' || !requireConfig) return ''
  const email = formValue.value.research2Email
  if (!String(email.to || '').trim()) return '请填写研究中心2报告收件人'
  if (!String(email.smtpHost || '').trim()) return '请填写研究中心2 SMTP 主机'
  if (!Number.isInteger(email.smtpPort) || email.smtpPort < 1 || email.smtpPort > 65535) return '研究中心2 SMTP 端口必须在 1 到 65535 之间'
  if (!String(email.smtpUsername || '').trim()) return '请填写研究中心2 SMTP 用户名'
  if (!String(email.smtpPassword || '').trim()) return '请填写研究中心2 SMTP 授权码'
  return ''
}

function getMinuteSourceConfigError() {
  if (formValue.value.privateMinute.enabled && !String(formValue.value.privateMinute.baseUrl || '').trim()) return '已启用私人分钟线接口，请填写调用 URL'
  if (formValue.value.privateMinute.enabled && !String(formValue.value.privateMinute.apiKey || '').trim()) return '已启用私人分钟线接口，请填写 API Key'
  const publicEnabled = formValue.value.akshareEnabled || formValue.value.sinaMinuteEnabled || formValue.value.tencentMinuteEnabled
  const privateOneMinuteEnabled = formValue.value.privateMinute.enabled && formValue.value.privateMinute.level === '1min'
  return publicEnabled || privateOneMinuteEnabled ? '' : '至少启用一个适用于 1 分钟图的数据接口'
}

function formatSaveTime(date = new Date()) {
  const pad = value => String(value).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

async function runPersist({showSuccess = false, notifyError = false, recordAutoSaveError = false} = {}) {
  if (recordAutoSaveError) autoSaveError.value = ''
  const validationError = getMinuteSourceConfigError()
    || getResearch2EmailConfigError()
  if (validationError) {
    if (notifyError) message.error(validationError)
    if (recordAutoSaveError) {
      autoSaveState.value = 'error'
      autoSaveError.value = validationError
    }
    return false
  }
  try {
    const result = await UpdateConfig(buildConfigPayload())
    if (String(result || '').includes('失败')) throw new Error(result)
    EventsEmit('updateSettings')
    if (showSuccess) message.success(result)
    if (recordAutoSaveError) {
      autoSaveState.value = 'saved'
      autoSaveLastSavedAt.value = formatSaveTime()
    }
    return true
  } catch (error) {
    const text = error?.message || String(error || '保存失败')
    if (notifyError) message.error(text)
    if (recordAutoSaveError) {
      autoSaveState.value = 'error'
      autoSaveError.value = text
    }
    return false
  }
}

function queueAutoSave() {
  if (!settingsLoaded.value) return Promise.resolve(false)
  queuedAutoSave = true
  if (activeSavePromise) return activeSavePromise
  activeSavePromise = (async () => {
    let saved = true
    while (queuedAutoSave) {
      queuedAutoSave = false
      autoSaveState.value = 'saving'
      saved = await runPersist({recordAutoSaveError: true})
      if (!saved) break
    }
    return saved
  })().finally(() => { activeSavePromise = null })
  return activeSavePromise
}

async function saveCurrentConfig(options = {}) {
  if (activeSavePromise) await activeSavePromise
  autoSaveState.value = 'saving'
  return runPersist({showSuccess: options.showSuccess, notifyError: options.notifyError, recordAutoSaveError: true})
}

async function testResearch2Email() {
  const validationError = getResearch2EmailConfigError(true)
  if (validationError) {
    message.error(validationError)
    return
  }
  research2EmailTesting.value = true
  try {
    if (!await saveCurrentConfig({notifyError: true})) return
    const result = await TestResearch2Email()
    message.success(result || '研究中心2测试邮件发送成功')
  } catch (error) {
    message.error(`测试邮件发送失败：${error?.message || error}`)
  } finally {
    research2EmailTesting.value = false
  }
}

function handleImmediateFieldChange() { queueAutoSave() }
function handleTextFieldBlur() { queueAutoSave() }

function exportConfig() {
  saveCurrentConfig({notifyError: true}).then(saved => saved ? ExportConfig() : null).then(result => {
    if (result) message.info(result)
  })
}

function importConfig() {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = '.json'
  input.onchange = event => {
    const reader = new FileReader()
    reader.onload = loadEvent => {
      try {
        applyConfigToForm(JSON.parse(loadEvent.target.result))
        queueAutoSave()
      } catch (error) {
        message.error(`配置文件无效：${error?.message || error}`)
      }
    }
    reader.readAsText(event.target.files[0])
  }
  input.click()
}

onMounted(async () => {
  try {
    applyConfigToForm(await GetConfig())
    settingsLoaded.value = true
  } catch (error) {
    message.error(`读取设置失败：${error?.message || error}`)
  }
})

onBeforeUnmount(() => message.destroyAll())
</script>

<template>
  <n-flex justify="left" style="text-align: left">
    <n-form ref="formRef" label-placement="left" label-align="left" style="width: 100%">
      <n-space vertical size="large">
        <n-card :title="() => h(NTag, {type: 'primary', bordered: false}, () => '通用设置')" size="small">
          <n-grid :cols="24" :x-gap="24">
            <n-form-item-gi :span="8" label="暗黑主题：" path="darkTheme">
              <n-switch v-model:value="formValue.darkTheme" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" label="启用基金模块：" path="enableFund">
              <n-switch v-model:value="formValue.enableFund" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
          </n-grid>
        </n-card>

        <n-card :title="() => h(NTag, {type: 'primary', bordered: false}, () => '数据接口设置')" size="small">
          <n-grid :cols="24" :x-gap="24">
            <n-form-item-gi :span="12" label="Tushare Token：" path="tushareToken">
              <n-input v-model:value="formValue.tushareToken" type="password" show-password-on="click"
                       placeholder="Tushare API Token" clearable @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="12" label="东财唯一标识：" path="qgqpBId">
              <n-input v-model:value="formValue.qgqpBId" placeholder="东财唯一标识" clearable @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" label="启动时更新基础信息：" path="updateBasicInfoOnStart">
              <n-switch v-model:value="formValue.updateBasicInfoOnStart" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" label="数据刷新间隔：" path="refreshInterval">
              <n-input-number v-model:value="formValue.refreshInterval" :min="1" @update:value="handleImmediateFieldChange">
                <template #suffix>秒</template>
              </n-input-number>
            </n-form-item-gi>
            <n-form-item-gi :span="8" label="长历史提示：" path="minuteLongHistoryHintEnabled">
              <n-switch v-model:value="formValue.minuteLongHistoryHintEnabled" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-gi :span="24">
              <n-divider title-placement="left">分钟线数据接口</n-divider>
              <MinuteProviderSettings
                  :form-value="formValue"
                  :akshare-minute-source-options="akshareMinuteSourceOptions"
                  :private-minute-proxy-mode-options="privateMinuteProxyModeOptions"
                  :private-minute-level-options="privateMinuteLevelOptions"
                  @immediate-change="handleImmediateFieldChange"
                  @text-blur="handleTextFieldBlur"
                  @move-provider="handleProviderMove"/>
            </n-gi>
          </n-grid>
        </n-card>

        <n-card :title="() => h(NTag, {type: 'primary', bordered: false}, () => settingsScope === 'research1' ? '资金补位策略' : 'AI 分析设置')" size="small">
          <n-grid :cols="24" :x-gap="24">
            <n-form-item-gi v-if="settingsScope === 'research2'" :span="24" label="研究中心2自动策略：" path="research2AutoEnabled">
              <n-switch v-model:value="formValue.research2AutoEnabled" @update:value="handleImmediateFieldChange"/>
              <n-text depth="3" style="margin-left: 12px">启动窗口为交易日 [09:50,13:00)，以实际启动前5个已闭合交易分钟为证据；报告在 13:00 前生成才进入模拟执行，13:00 起生成的推荐标记为“仅分析”且不计收益。</n-text>
            </n-form-item-gi>
            <n-form-item-gi :span="24" label="实验市场证据：" path="experimentalEvidenceEnabled">
              <n-switch v-model:value="formValue.experimentalEvidenceEnabled" @update:value="handleImmediateFieldChange"/>
              <n-text depth="3" style="margin-left: 12px">默认关闭；开启后两套研究会接入实验市场证据并可能改变研究结果，市场行情页面不受影响。</n-text>
            </n-form-item-gi>
            <template v-if="settingsScope === 'research1'">
              <n-form-item-gi :span="6" label="资金补位：" path="capitalDeployment.enabled">
                <n-switch v-model:value="formValue.capitalDeployment.enabled" @update:value="handleImmediateFieldChange"/>
              </n-form-item-gi>
              <n-form-item-gi :span="6" label="目标资金利用率：" path="capitalDeployment.targetCapitalUtilization">
                <n-input-number v-model:value="formValue.capitalDeployment.targetCapitalUtilization" :min="50" :max="90" :step="1" @update:value="handleImmediateFieldChange">
                  <template #suffix>%</template>
                </n-input-number>
              </n-form-item-gi>
              <n-form-item-gi :span="6" label="单轮立即买入上限：" path="capitalDeployment.maxImmediateBuysPerRun">
                <n-input-number v-model:value="formValue.capitalDeployment.maxImmediateBuysPerRun" :min="1" :max="2" @update:value="handleImmediateFieldChange">
                  <template #suffix>只</template>
                </n-input-number>
              </n-form-item-gi>
              <n-form-item-gi :span="6" label="资金缺口重分析：" path="capitalDeployment.reanalysisIntervalMinutes">
                <n-input-number v-model:value="formValue.capitalDeployment.reanalysisIntervalMinutes" :min="5" :max="120"
                                @update:value="handleImmediateFieldChange">
                  <template #suffix>分钟</template>
                </n-input-number>
              </n-form-item-gi>
              <n-form-item-gi :span="8" label="持仓复查开始：" path="capitalDeployment.reviewStartTime">
                <n-input v-model:value="formValue.capitalDeployment.reviewStartTime" placeholder="09:50" @blur="handleTextFieldBlur"/>
              </n-form-item-gi>
              <n-form-item-gi :span="8" label="持仓复查间隔：" path="capitalDeployment.reviewIntervalMinutes">
                <n-input-number v-model:value="formValue.capitalDeployment.reviewIntervalMinutes" :min="5" :max="120" @update:value="handleImmediateFieldChange">
                  <template #suffix>分钟</template>
                </n-input-number>
              </n-form-item-gi>
            </template>
            <n-gi :span="24">
              <n-alert v-if="settingsScope === 'research1'" type="info" :show-icon="false">
                卖出或启动发现至少 5 万元可部署资金时自动触发完整分析；每轮最多立即买入 {{ formValue.capitalDeployment.maxImmediateBuysPerRun }} 只，仍有资金缺口会在 {{ formValue.capitalDeployment.reanalysisIntervalMinutes }} 分钟后重新分析，14:25 后不再启动新分析。资金保留额为净资产的 10% 且不少于 5 万元。持仓仍按每只股票本轮完成时间独立计算复查间隔。
              </n-alert>
              <AiConfigSettings
                  :form-value="formValue"
                  :ai-protocol-options="aiProtocolOptions"
                  :ai-config-row-key="aiConfigRowKey"
                  :ai-config-test-state="aiConfigTestState"
                  @immediate-change="handleImmediateFieldChange"
                  @text-blur="handleTextFieldBlur"
                  @add-ai-config="addAiConfig"
                  @remove-ai-config="removeAiConfig"
                  @test-ai-config="testAiConfig"
                  @move-ai-config="handleAiConfigMove"/>
            </n-gi>
          </n-grid>
        </n-card>

        <n-card v-if="settingsScope === 'research2'" :title="() => h(NTag, {type: 'primary', bordered: false}, () => '研究中心2报告邮件')" size="small">
          <n-grid :cols="24" :x-gap="24">
            <n-form-item-gi :span="24" label="自动发送报告：" path="research2Email.enabled">
              <n-switch v-model:value="formValue.research2Email.enabled" @update:value="handleImmediateFieldChange"/>
              <n-text depth="3" style="margin-left:12px">报告落库并完成交易处理后异步发送；失败后最多重试3次。</n-text>
            </n-form-item-gi>
            <n-form-item-gi :span="12" label="收件人：" path="research2Email.to">
              <n-input v-model:value="formValue.research2Email.to" type="textarea" :autosize="{minRows:2,maxRows:4}"
                       placeholder="多个地址可用逗号、分号或换行分隔" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="12" label="发件人：" path="research2Email.from">
              <n-input v-model:value="formValue.research2Email.from" placeholder="留空时使用 SMTP 用户名" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="10" label="SMTP 主机：" path="research2Email.smtpHost">
              <n-input v-model:value="formValue.research2Email.smtpHost" placeholder="smtp.example.com" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="端口：" path="research2Email.smtpPort">
              <n-input-number v-model:value="formValue.research2Email.smtpPort" :min="1" :max="65535" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="10" label="SMTP 用户名：" path="research2Email.smtpUsername">
              <n-input v-model:value="formValue.research2Email.smtpUsername" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="12" label="SMTP 授权码：" path="research2Email.smtpPassword">
              <n-input v-model:value="formValue.research2Email.smtpPassword" type="password" show-password-on="click" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="12">
              <n-button type="primary" secondary :loading="research2EmailTesting" @click="testResearch2Email">发送测试邮件</n-button>
            </n-form-item-gi>
            <n-gi :span="24"><n-alert type="info" :show-icon="false">支持465隐式TLS及STARTTLS。测试邮件只验证配置，不创建研究记录或模拟交易。</n-alert></n-gi>
          </n-grid>
        </n-card>

        <n-card :title="() => h(NTag, {type: 'primary', bordered: false}, () => '配置管理')" size="small">
          <n-space vertical align="center">
            <n-space>
              <n-button type="info" @click="exportConfig">导出配置</n-button>
              <n-button type="warning" @click="importConfig">导入配置</n-button>
            </n-space>
            <n-text type="error">导出的 JSON 包含完整明文 API Key、Token 和 SMTP 授权码，请仅保存在可信设备。</n-text>
            <n-text depth="3" v-if="autoSaveState === 'saving'">正在自动保存...</n-text>
            <n-text type="success" v-else-if="autoSaveState === 'saved'">已自动保存 {{ autoSaveLastSavedAt }}</n-text>
            <n-text type="error" v-else-if="autoSaveState === 'error'">自动保存失败：{{ autoSaveError }}</n-text>
          </n-space>
        </n-card>
      </n-space>
    </n-form>
  </n-flex>
</template>
