<script setup>
import {h, onBeforeUnmount, onMounted, ref} from 'vue'
import {NTag, useMessage} from 'naive-ui'
import {GetConfig, TestAIConfig, UpdateConfig} from '../services/settings-api'
import {ExportConfig} from '../services/exports-api'
import {EventsEmit} from '../services/browser-runtime.mjs'
import MinuteProviderSettings from './settings/MinuteProviderSettings.vue'
import AiConfigSettings from './settings/AiConfigSettings.vue'

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
  aiAnalysis: {
    autoEnabled: true,
    times: '09:30,11:30,14:30',
    reviewStartTime: '09:50',
    reviewIntervalMinutes: 15,
  },
  research2AutoEnabled: true,
})

const aiConfigTestStates = ref({})
const settingsLoaded = ref(false)
const persistedConfig = ref({})
const autoSaveState = ref('idle')
const autoSaveError = ref('')
const autoSaveLastSavedAt = ref('')
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
  formValue.value.aiAnalysis = {
    autoEnabled: config?.aiAnalysisAutoEnabled ?? config?.aiAnalysisEnabled ?? true,
    times: config?.aiAnalysisTimes || '09:30,11:30,14:30',
    reviewStartTime: config?.aiReviewStartTime || '09:50',
    reviewIntervalMinutes: config?.aiReviewIntervalMinutes || 15,
  }
  formValue.value.research2AutoEnabled = config?.research2AutoEnabled !== false
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
    aiAnalysisAutoEnabled: formValue.value.aiAnalysis.autoEnabled,
    aiAnalysisConfigId: primaryAiConfigId(),
    aiAnalysisTimes: formValue.value.aiAnalysis.times,
    aiReviewStartTime: formValue.value.aiAnalysis.reviewStartTime,
    aiReviewIntervalMinutes: formValue.value.aiAnalysis.reviewIntervalMinutes,
    research2AutoEnabled: formValue.value.research2AutoEnabled,
  }
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

        <n-card :title="() => h(NTag, {type: 'primary', bordered: false}, () => 'AI 分析设置')" size="small">
          <n-grid :cols="24" :x-gap="24">
            <n-form-item-gi :span="24" label="研究中心2自动策略：" path="research2AutoEnabled">
              <n-switch v-model:value="formValue.research2AutoEnabled" @update:value="handleImmediateFieldChange"/>
              <n-text depth="3" style="margin-left: 12px">固定 09:50 开始采集、09:55 冻结数据，10:00 模拟买入，下一交易日 10:00 模拟卖出。</n-text>
            </n-form-item-gi>
            <n-form-item-gi :span="6" label="自动分析：" path="aiAnalysis.autoEnabled">
              <n-switch v-model:value="formValue.aiAnalysis.autoEnabled" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="18" label="自动分析时间：" path="aiAnalysis.times">
              <n-input v-model:value="formValue.aiAnalysis.times" placeholder="09:30,11:30,14:30" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" label="持仓复查开始：" path="aiAnalysis.reviewStartTime">
              <n-input v-model:value="formValue.aiAnalysis.reviewStartTime" placeholder="09:50" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" label="持仓复查间隔：" path="aiAnalysis.reviewIntervalMinutes">
              <n-input-number v-model:value="formValue.aiAnalysis.reviewIntervalMinutes" :min="5" :max="120"
                              @update:value="handleImmediateFieldChange">
                <template #suffix>分钟</template>
              </n-input-number>
            </n-form-item-gi>
            <n-gi :span="24">
              <n-alert type="info" :show-icon="false">
                “自动分析”只控制定时触发；关闭后，研究中心的手动分析仍使用下方同一组模型。错过的最近自动分析节点会在开盘时补跑；自动分析或持仓复查失败后每 5 分钟重试至当日收盘。持仓首轮从开始时间触发，之后每只股票按本轮完成时间独立计算复查间隔。
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
