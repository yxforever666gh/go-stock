<script setup>
import {h, onBeforeUnmount, onMounted, ref} from "vue";
import {
  AddPrompt, DelPrompt,
  ExportConfig,
  GetConfig,
  GetPromptTemplates,
  TestAIConfig,
  UpdateConfig
} from "../services/app-api";
import {NTag, useMessage} from "naive-ui";
import {models} from "../../wailsjs/go/models";
import {EventsEmit} from "../services/browser-runtime.mjs";
import MinuteProviderSettings from "./settings/MinuteProviderSettings.vue";
import AiConfigSettings from "./settings/AiConfigSettings.vue";

const message = useMessage()

const formRef = ref(null)
const formValue = ref({
  ID: 1,
  tushareToken: '',
  updateBasicInfoOnStart: false,
  refreshInterval: 1,
  openAI: {
    enable: false,
    aiConfigs: [], // AI配置列表
    prompt: "",
    questionTemplate: "{{stockName}}分析和总结",
    crawlTimeOut: 30,
    kDays: 30,
    httpProxy:"",
    httpProxyEnabled:false,
  },
  browserPath: '',
  darkTheme: true,
  minuteProviderMode: 'public',
  minuteLongHistoryHintEnabled: true,
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
  enableFund: false,
  httpProxy:"",
  httpProxyEnabled:false,
  forceNoProxyForFetch: true,
  aiAnalysis: {
    enabled: true,
    configId: null,
    times: '09:30,11:30,14:30',
  },
  qgqpBId: '',
})
const aiConfigTestStates = ref({})
const settingsLoaded = ref(false)
const persistedConfig = ref({})
const autoSaveState = ref('idle')
const autoSaveError = ref('')
const autoSaveLastSavedAt = ref('')
const minuteProviderModeOptions = [
  {label: '公共源优先', value: 'public'},
  {label: '私人分钟线来源', value: 'private'},
]
const akshareMinuteSourceOptions = [
  {label: '自动', value: 'auto'},
  {label: '新浪', value: 'sina'},
  {label: '东方财富', value: 'em'},
]
const privateMinuteProxyModeOptions = [
  {label: '强制直连', value: 'disable'},
  {label: '跟随系统代理', value: 'inherit'},
  {label: '使用设置页代理', value: 'settings'},
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
let activeSavePromise = null
let queuedAutoSave = false
let nextAiConfigLocalKey = 1
const aiConfigLocalKeys = new WeakMap()

function normalizeAiProtocol(value) {
  const text = String(value || '').trim()
  if (text === 'openai_responses' || text === 'anthropic_messages') {
    return text
  }
  return 'chat_completions'
}

function normalizeAiConfigs(configs) {
  return (configs || []).map((item, index) => ({
    ...item,
    sort: index + 1,
    disabled: item?.disabled === true,
    apiProtocol: normalizeAiProtocol(item?.apiProtocol),
  }))
}

function primaryAiConfigId(configs = formValue.value.openAI.aiConfigs) {
  return (configs || []).find(item => item?.disabled !== true)?.ID || 0
}

function applyConfigToForm(config) {
  const normalizedAiConfigs = Array.isArray(config?.aiConfigs)
      ? normalizeAiConfigs(config.aiConfigs)
      : formValue.value.openAI.aiConfigs
  persistedConfig.value = {
    ...(config || {}),
    aiConfigs: normalizedAiConfigs,
  }
  formValue.value.ID = config?.ID || formValue.value.ID
  formValue.value.tushareToken = config?.tushareToken || ''
  formValue.value.updateBasicInfoOnStart = config?.updateBasicInfoOnStart === true
  formValue.value.refreshInterval = config?.refreshInterval
  formValue.value.openAI = {
    enable: config?.openAiEnable === true,
    aiConfigs: normalizedAiConfigs,
    prompt: config?.prompt || '',
    questionTemplate: config?.questionTemplate || '{{stockName}}分析和总结',
    crawlTimeOut: config?.crawlTimeOut,
    kDays: config?.kDays,
    httpProxy: '',
    httpProxyEnabled: false,
  }
  formValue.value.browserPath = config?.browserPath || ''
  formValue.value.darkTheme = config?.darkTheme === true
  formValue.value.minuteProviderMode = config?.minuteProviderMode || 'public'
  formValue.value.minuteLongHistoryHintEnabled = config?.minuteLongHistoryHintEnabled !== false
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
  formValue.value.enableFund = config?.enableFund === true
  formValue.value.httpProxy = config?.httpProxy || ''
  formValue.value.httpProxyEnabled = config?.httpProxyEnabled === true
  formValue.value.forceNoProxyForFetch = config?.forceNoProxyForFetch !== false
  formValue.value.aiAnalysis = {
    enabled: config?.aiAnalysisEnabled !== false,
    configId: primaryAiConfigId(normalizedAiConfigs) || null,
    times: config?.aiAnalysisTimes || '09:30,11:30,14:30',
  }
  formValue.value.qgqpBId = config?.qgqpBId || ''
}

function aiConfigRowKey(aiConfig) {
  if (aiConfig?.ID) {
    return `id-${aiConfig.ID}`
  }
  if (!aiConfig || typeof aiConfig !== 'object') {
    return `new-${nextAiConfigLocalKey++}`
  }
  if (!aiConfigLocalKeys.has(aiConfig)) {
    aiConfigLocalKeys.set(aiConfig, `new-${nextAiConfigLocalKey++}`)
  }
  return aiConfigLocalKeys.get(aiConfig)
}

function renumberAiConfigSorts() {
  const configs = formValue.value.openAI.aiConfigs || []
  configs.forEach((item, index) => {
    item.sort = index + 1
    item.apiProtocol = normalizeAiProtocol(item.apiProtocol)
  })
}

// 添加一个新的AI配置到列表
function addAiConfig() {
  formValue.value.openAI.aiConfigs.push(new models.AIConfig({
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
    httpProxy:"",
    httpProxyEnabled:false,
  }));
  renumberAiConfigSorts()
  queueAutoSave()
}

// 从列表中移除一个AI配置
function removeAiConfig(index) {
  // 使用filter创建新数组确保响应式更新
  formValue.value.openAI.aiConfigs = formValue.value.openAI.aiConfigs.filter((_, i) => i !== index);
  renumberAiConfigSorts()
  queueAutoSave()
}

function moveAiConfig(sourceIndex, targetIndex) {
  const configs = formValue.value.openAI.aiConfigs || []
  if (
      sourceIndex === null ||
      targetIndex === null ||
      sourceIndex === targetIndex ||
      sourceIndex < 0 ||
      targetIndex < 0 ||
      sourceIndex >= configs.length ||
      targetIndex >= configs.length
  ) {
    return false
  }
  const [moved] = configs.splice(sourceIndex, 1)
  configs.splice(targetIndex, 0, moved)
  renumberAiConfigSorts()
  return true
}

function handleAiConfigMove(sourceIndex, targetIndex) {
  const moved = moveAiConfig(sourceIndex, targetIndex)
  if (moved) {
    queueAutoSave()
  }
}

function formatSaveTime(date = new Date()) {
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function markAutoSaveSaved() {
  autoSaveState.value = 'saved'
  autoSaveError.value = ''
  autoSaveLastSavedAt.value = formatSaveTime()
}

function handleImmediateFieldChange() {
  queueAutoSave()
}

function handleTextFieldBlur() {
  queueAutoSave()
}


const promptTemplates = ref([])
onMounted(() => {
  GetConfig().then(res => {
    applyConfigToForm(res)
    settingsLoaded.value = true
  })

  GetPromptTemplates("", "").then(res => {
    promptTemplates.value = res
  })
})
onBeforeUnmount(() => {
  message.destroyAll()
})

function buildConfigPayload() {
  renumberAiConfigSorts()
  return new models.SettingConfig({
	...persistedConfig.value,
    ID: formValue.value.ID,
    updateBasicInfoOnStart: formValue.value.updateBasicInfoOnStart,
    refreshInterval: formValue.value.refreshInterval,
    openAiEnable: formValue.value.openAI.enable,
    aiConfigs: formValue.value.openAI.aiConfigs,
    // 序列化aiConfigs列表以传递给后端
    tushareToken: formValue.value.tushareToken,
    prompt: formValue.value.openAI.prompt,
    questionTemplate: formValue.value.openAI.questionTemplate,
    crawlTimeOut: formValue.value.openAI.crawlTimeOut,
    kDays: formValue.value.openAI.kDays,
    browserPath: formValue.value.browserPath,
    darkTheme: formValue.value.darkTheme,
    minuteProviderMode: formValue.value.minuteProviderMode,
    minuteLongHistoryHintEnabled: formValue.value.minuteLongHistoryHintEnabled,
    privateMinuteEnabled: formValue.value.privateMinute.enabled,
    privateMinuteBaseUrl: formValue.value.privateMinute.baseUrl,
    privateMinuteApiKey: formValue.value.privateMinute.apiKey,
    privateMinuteTimeoutSec: formValue.value.privateMinute.timeoutSec,
    privateMinuteMinIntervalMs: formValue.value.privateMinute.minIntervalMs,
    privateMinuteProxyMode: formValue.value.privateMinute.proxyMode,
    privateMinuteLevel: formValue.value.privateMinute.level,
    akshareEnabled: formValue.value.akshareEnabled,
    sinaMinuteEnabled: formValue.value.sinaMinuteEnabled,
    tencentMinuteEnabled: formValue.value.tencentMinuteEnabled,
    akshareMinuteSourceMode: formValue.value.akshareMinuteSourceMode,
    enableFund: formValue.value.enableFund,
    httpProxy:formValue.value.httpProxy,
    httpProxyEnabled:formValue.value.httpProxyEnabled,
    forceNoProxyForFetch: formValue.value.forceNoProxyForFetch,
    aiAnalysisEnabled: formValue.value.aiAnalysis.enabled,
    // The first enabled row is the primary model; the remaining enabled rows
    // are attempted from top to bottom as fallbacks.
    aiAnalysisConfigId: primaryAiConfigId(),
    aiAnalysisTimes: formValue.value.aiAnalysis.times,
    qgqpBId: formValue.value.qgqpBId
  })
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
  aiConfigTestStates.value = {
    ...aiConfigTestStates.value,
    [key]: {loading: true, result: null}
  }
  try {
    const saved = await saveCurrentConfig({notifyError: true})
    if (!saved) {
      aiConfigTestStates.value = {
        ...aiConfigTestStates.value,
        [key]: {loading: false, result: {success: false, message: autoSaveError.value || '保存失败'}}
      }
      return
    }
    const latest = await GetConfig()
    formValue.value.openAI.aiConfigs = normalizeAiConfigs(latest.aiConfigs || [])
    const savedConfig = formValue.value.openAI.aiConfigs[index]
    const savedKey = aiConfigTestKey(savedConfig, index)
    if (!savedConfig?.ID) {
      aiConfigTestStates.value = {
        ...aiConfigTestStates.value,
        [key]: {loading: false, result: null},
        [savedKey]: {loading: false, result: {success: false, message: '请先保存 AI 配置后再测试'}}
      }
      return
    }
    aiConfigTestStates.value = {
      ...aiConfigTestStates.value,
      [key]: {loading: false, result: null},
      [savedKey]: {loading: true, result: null}
    }
    const result = await TestAIConfig(Number(savedConfig.ID))
    aiConfigTestStates.value = {
      ...aiConfigTestStates.value,
      [savedKey]: {loading: false, result}
    }
    if (result?.success) {
      message.success(`模型测试成功：${result.contentPreview || result.message}`)
    } else {
      message.error(result?.message || '模型测试失败')
    }
  } catch (error) {
    aiConfigTestStates.value = {
      ...aiConfigTestStates.value,
      [key]: {loading: false, result: {success: false, message: error?.message || String(error || '模型测试失败')}}
    }
    message.error(error?.message || String(error || '模型测试失败'))
  }
}

function getMinuteSourceConfigError() {
  if (formValue.value.minuteProviderMode === 'public') {
    if (!formValue.value.akshareEnabled && !formValue.value.sinaMinuteEnabled && !formValue.value.tencentMinuteEnabled) {
      return "公共分钟线模式下，至少保留一个公共数据源"
    }
    return ""
  }

  if (!formValue.value.privateMinute.enabled) {
    return "私人分钟线模式下，请先启用私人分钟线来源"
  }
  if (!String(formValue.value.privateMinute.baseUrl || "").trim()) {
    return "请填写私人分钟线来源的调用 URL"
  }
  if (!String(formValue.value.privateMinute.apiKey || "").trim()) {
    return "请填写私人分钟线来源的 API Key"
  }
  return ""
}

async function runPersist(options = {}) {
  const showSuccess = options.showSuccess === true
  const notifyError = options.notifyError === true
  const recordAutoSaveError = options.recordAutoSaveError === true
  if (recordAutoSaveError) {
    autoSaveError.value = ''
  }

  const minuteSourceError = getMinuteSourceConfigError()
  if (minuteSourceError) {
    if (notifyError) {
      message.error(minuteSourceError)
    }
    if (recordAutoSaveError) {
      autoSaveError.value = minuteSourceError
      autoSaveState.value = 'error'
    }
    return false
  }

  const config = buildConfigPayload()
  const res = await UpdateConfig(config)
  if (String(res || '').includes('失败')) {
    if (notifyError) {
      message.error(res)
    }
    if (recordAutoSaveError) {
      autoSaveError.value = res
      autoSaveState.value = 'error'
    }
    return false
  }
  if (showSuccess) {
    message.success(res)
  }
  EventsEmit("updateSettings");
  if (recordAutoSaveError) {
    markAutoSaveSaved()
  }
  return true
}

function queueAutoSave() {
  if (!settingsLoaded.value) {
    return Promise.resolve(false)
  }
  queuedAutoSave = true
  if (activeSavePromise) {
    return activeSavePromise
  }
  activeSavePromise = (async () => {
    let saved = true
    while (queuedAutoSave) {
      queuedAutoSave = false
      autoSaveState.value = 'saving'
      saved = await runPersist({
        notifyError: false,
        recordAutoSaveError: true
      })
      if (!saved) {
        break
      }
    }
    return saved
  })().finally(() => {
    activeSavePromise = null
  })
  return activeSavePromise
}

async function saveCurrentConfig(options = {}) {
  if (activeSavePromise) {
    await activeSavePromise
  }
  autoSaveState.value = 'saving'
  const saved = await runPersist({
    showSuccess: options.showSuccess === true,
    notifyError: options.notifyError === true,
    recordAutoSaveError: true
  })
  if (!saved && !autoSaveError.value) {
    autoSaveState.value = 'error'
    autoSaveError.value = '自动保存失败'
  }
  return saved
}

function exportConfig() {
  saveCurrentConfig({notifyError: true}).then(saved => {
    if (!saved) {
      return null
    }
    return ExportConfig()
  }).then(res => {
    if (!res) {
      return
    }
    message.info(res)
  })
}

function importConfig() {
  let input = document.createElement('input');
  input.type = 'file';
  input.accept = '.json';
  input.onchange = (e) => {
    let file = e.target.files[0];
    let reader = new FileReader();
    reader.onload = (e) => {
      let config = JSON.parse(e.target.result);
      applyConfigToForm(config)
      queueAutoSave()
    };
    reader.readAsText(file);
  };
  input.click();
}


window.onerror = function (event, source, lineno, colno, error) {
  EventsEmit("frontendError", {
    page: "settings.vue",
    message: event,
    source: source,
    lineno: lineno,
    colno: colno,
    error: error ? error.stack : null
  });
  return true;
};

const showManagePromptsModal = ref(false)
const promptTypeOptions = [
  {label: "模型系统Prompt", value: '模型系统Prompt'},
  {label: "模型用户Prompt", value: '模型用户Prompt'},]
const formPromptRef = ref(null)
const formPrompt = ref({
  ID: 0,
  Name: '',
  Content: '',
  Type: '',
})

function managePrompts() {
  formPrompt.value.ID = 0
  showManagePromptsModal.value = true
}

function savePrompt() {
  AddPrompt(formPrompt.value).then(res => {
    message.success(res)
    GetPromptTemplates("", "").then(res => {
      promptTemplates.value = res
    })
    showManagePromptsModal.value = false
  })
}

function editPrompt(prompt) {
  formPrompt.value.ID = prompt.ID
  formPrompt.value.Name = prompt.name
  formPrompt.value.Content = prompt.content
  formPrompt.value.Type = prompt.type
  showManagePromptsModal.value = true
}

function deletePrompt(ID) {
  DelPrompt(ID).then(res => {
    message.success(res)
    GetPromptTemplates("", "").then(res => {
      promptTemplates.value = res
    })
  })
}
</script>

<template>
  <n-flex justify="left" style="text-align: left; --wails-draggable:no-drag">
    <n-form ref="formRef" :label-placement="'left'" :label-align="'left'">
      <n-space vertical size="large">
        <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => '基础设置')" size="small">
          <n-grid :cols="24" :x-gap="24" style="text-align: left">
            <n-form-item-gi :span="10" label="Tushare Token：" path="tushareToken">
              <n-input type="text" placeholder="Tushare api token" v-model:value="formValue.tushareToken" clearable @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="启动时更新基础信息：" path="updateBasicInfoOnStart">
              <n-switch v-model:value="formValue.updateBasicInfoOnStart" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="数据刷新间隔：" path="refreshInterval">
              <n-input-number v-model:value="formValue.refreshInterval" placeholder="请输入数据刷新间隔(秒)" @update:value="handleImmediateFieldChange">
                <template #suffix>秒</template>
              </n-input-number>
            </n-form-item-gi>
            <n-form-item-gi :span="6" label="暗黑主题：" path="darkTheme">
              <n-switch v-model:value="formValue.darkTheme" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="10" label="浏览器安装路径：" path="browserPath">
              <n-input type="text" placeholder="浏览器安装路径" v-model:value="formValue.browserPath" clearable @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="11" label="东财唯一标识：" path="qgqpBId">
              <n-input type="text" placeholder="东财唯一标识" v-model:value="formValue.qgqpBId" clearable @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
          </n-grid>
        </n-card>

        <MinuteProviderSettings
            :form-value="formValue"
            :minute-provider-mode-options="minuteProviderModeOptions"
            :akshare-minute-source-options="akshareMinuteSourceOptions"
            :private-minute-proxy-mode-options="privateMinuteProxyModeOptions"
            :private-minute-level-options="privateMinuteLevelOptions"
            @immediate-change="handleImmediateFieldChange"
            @text-blur="handleTextFieldBlur"
        />

        <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => 'AI设置')" size="small">
          <n-grid :cols="24" :x-gap="24" style="text-align: left;">
            <n-form-item-gi :span="24" label="AI诊股：" path="openAI.enable">
              <n-switch v-model:value="formValue.openAI.enable" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>

            <n-form-item-gi :span="6" v-if="formValue.openAI.enable" label="Crawler Timeout(秒)"
                            title="资讯采集超时时间(秒)" path="openAI.crawlTimeOut">
              <n-input-number min="30" step="1" v-model:value="formValue.openAI.crawlTimeOut" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" v-if="formValue.openAI.enable" title="天数越多消耗tokens越多"
                            label="日K线数据(天)" path="openAI.kDays">
              <n-input-number min="30" step="1" max="60" v-model:value="formValue.openAI.kDays" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="2" label="爬虫http代理" path="httpProxyEnabled">
              <n-switch v-model:value="formValue.httpProxyEnabled" :disabled="formValue.forceNoProxyForFetch" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="抓取强制直连" path="forceNoProxyForFetch">
              <n-switch v-model:value="formValue.forceNoProxyForFetch" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" v-if="formValue.httpProxyEnabled && !formValue.forceNoProxyForFetch" title="http代理地址"
                            label="http代理地址" path="httpProxy">
              <n-input type="text" placeholder="爬虫http代理地址" v-model:value="formValue.httpProxy" clearable @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-gi :span="24" v-if="formValue.forceNoProxyForFetch">
              <n-tag type="success" :bordered="false">已强制关闭所有信息抓取代理，网页抓取与接口抓取都会直连</n-tag>
            </n-gi>


            <n-gi :span="24" v-if="formValue.openAI.enable">
              <n-divider title-placement="left">Prompt 内容设置</n-divider>
            </n-gi>
            <n-form-item-gi :span="12" v-if="formValue.openAI.enable" label="模型系统 Prompt" path="openAI.prompt">
              <n-input v-model:value="formValue.openAI.prompt" type="textarea" :show-count="true"
                       placeholder="请输入系统prompt" :autosize="{ minRows: 4, maxRows: 8 }" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>
            <n-form-item-gi :span="12" v-if="formValue.openAI.enable" label="模型用户 Prompt"
                            path="openAI.questionTemplate">
              <n-input v-model:value="formValue.openAI.questionTemplate" type="textarea" :show-count="true"
                       placeholder="请输入用户prompt:例如{{stockName}}[{{stockCode}}]分析和总结"
                       :autosize="{ minRows: 4, maxRows: 8 }" @blur="handleTextFieldBlur"/>
            </n-form-item-gi>

            <n-gi :span="24">
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
                  @move-ai-config="handleAiConfigMove"
              />
            </n-gi>

            <n-gi :span="24">
              <n-divider/>
            </n-gi>

            <n-gi :span="24">
              <n-space vertical>
                <n-space justify="center">
                  <n-button type="warning" @click="managePrompts">管理提示词模板</n-button>
                  <n-button type="info" @click="exportConfig">导出配置</n-button>
                  <n-button type="error" @click="importConfig">导入配置</n-button>
                </n-space>
                <n-text type="error" style="text-align: center">
                  导出的 JSON 包含完整明文 API Key、Token 和 SMTP 授权码，请仅保存在可信设备。
                </n-text>
                <n-flex justify="center">
                  <n-text depth="3" v-if="autoSaveState === 'saving'">正在自动保存...</n-text>
                  <n-text depth="3" type="success" v-else-if="autoSaveState === 'saved'">已自动保存 {{ autoSaveLastSavedAt }}</n-text>
                  <n-text depth="3" type="error" v-else-if="autoSaveState === 'error'">自动保存失败：{{ autoSaveError }}</n-text>
                </n-flex>

                <n-flex justify="start" style="margin-top: 10px" v-if="promptTemplates.length > 0">
                  <n-tag :bordered="false" type="warning">提示词模板:</n-tag>
                  <n-tag size="medium" secondary v-for="prompt in promptTemplates" closable
                         @close="deletePrompt(prompt.ID)" @click="editPrompt(prompt)" :title="prompt.content"
                         :type="prompt.type === '模型系统Prompt' ? 'success' : 'info'" :bordered="false">{{
                      prompt.name
                    }}
                  </n-tag>
                </n-flex>
              </n-space>
            </n-gi>

          </n-grid>
        </n-card>

        <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => 'AI 分析')" size="small">
          <n-grid :cols="24" :x-gap="24" style="text-align: left">
            <n-form-item-gi :span="4" label="启用：" path="aiAnalysis.enabled">
              <n-switch v-model:value="formValue.aiAnalysis.enabled" @update:value="handleImmediateFieldChange"/>
            </n-form-item-gi>
            <n-form-item-gi :span="20" label="分析时间：" path="aiAnalysis.times">
              <n-input
                  v-model:value="formValue.aiAnalysis.times"
                  placeholder="09:30,11:30,14:30"
                  @blur="handleTextFieldBlur"
              />
            </n-form-item-gi>
            <n-gi :span="24">
              <n-text depth="3">模型按上方“回退顺序”从上到下调用，关闭的模型会直接跳过。仅沪深交易日自动运行；有待买入任务或未卖出持仓时跳过新分析。</n-text>
            </n-gi>
          </n-grid>
        </n-card>
      </n-space>
    </n-form>
  </n-flex>

  <n-modal v-model:show="showManagePromptsModal" closable :mask-closable="false">
    <n-card style="width: 800px; height: 600px; text-align: left" :bordered="false"
            :title="(formPrompt.ID > 0 ? '修改' : '添加') + '提示词'" size="huge" role="dialog" aria-modal="true">
      <n-form ref="formPromptRef" :label-placement="'left'" :label-align="'left'">
        <n-form-item label="名称">
          <n-input v-model:value="formPrompt.Name" placeholder="请输入提示词名称"/>
        </n-form-item>
        <n-form-item label="类型">
          <n-select v-model:value="formPrompt.Type" :options="promptTypeOptions" placeholder="请选择提示词类型"/>
        </n-form-item>
        <n-form-item label="内容">
          <n-input v-model:value="formPrompt.Content" type="textarea" :show-count="true" placeholder="请输入prompt"
                   :autosize="{ minRows: 12, maxRows: 12, }"/>
        </n-form-item>
      </n-form>
      <template #footer>
        <n-flex justify="end">
          <n-button type="primary" @click="savePrompt">保存</n-button>
          <n-button type="warning" @click="showManagePromptsModal = false">取消</n-button>
        </n-flex>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.cardHeaderClass {
  font-size: 16px;
  font-weight: bold;
  color: red;
}
</style>
