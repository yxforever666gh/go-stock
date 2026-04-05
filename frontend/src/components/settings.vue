<script setup>
import {h, onBeforeUnmount, onMounted, ref} from "vue";
import {
  AddPrompt, DelPrompt,
  ExportConfig,
  GetConfig,
  GetEmailSendLogList,
  GetPromptTemplates,
  SendLatestAIAnalysisReportNow,
  SendYieldEmailCSVNow,
  SendYieldEmailTestMessage,
  UpdateConfig
} from "../services/app-api";
import {NTag, useMessage} from "naive-ui";
import {data, models} from "../../wailsjs/go/models";
import {EventsEmit} from "../../wailsjs/runtime";

const message = useMessage()

const formRef = ref(null)
const formValue = ref({
  ID: 1,
  tushareToken: '',
  yieldEmail: {
    enable: false,
    to: '',
    from: '',
    smtpHost: '',
    smtpPort: 465,
    smtpUsername: '',
    smtpPassword: '',
    cronEnabled: false,
    cronTimes: ''
  },
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
  sponsorCode: "",
  httpProxy:"",
  httpProxyEnabled:false,
  forceNoProxyForFetch: true,
  enableAgent: false,
  qgqpBId: '',
  marketSummaryCronEnabled: true,
  marketSummaryCronTimes: '09:30,11:30,18:00',
})
const yieldEmailTestSending = ref(false)
const yieldEmailCsvSending = ref(false)
const yieldEmailAiReportSending = ref(false)
const emailSendLogsLoading = ref(false)
const emailSendLogs = ref([])
const emailSendLogPage = ref(1)
const emailSendLogTotalPages = ref(1)
const emailSendLogTotal = ref(0)
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

// 添加一个新的AI配置到列表
function addAiConfig() {
  formValue.value.openAI.aiConfigs.push(new data.AIConfig({
    name: '',
    baseUrl: 'https://api.deepseek.com',
    apiKey: '',
    modelName: 'deepseek-chat',
    temperature: 0.1,
    maxTokens: 4096,
    timeOut: 300,
    httpProxy:"",
    httpProxyEnabled:false,
  }));
}

// 从列表中移除一个AI配置
function removeAiConfig(index) {
  const originalCount = formValue.value.openAI.aiConfigs.length;
  // 使用filter创建新数组确保响应式更新
  formValue.value.openAI.aiConfigs = formValue.value.openAI.aiConfigs.filter((_, i) => i !== index);
}


const promptTemplates = ref([])
onMounted(() => {
  GetConfig().then(res => {
    formValue.value.ID = res.ID
    formValue.value.tushareToken = res.tushareToken
    formValue.value.yieldEmail = {
      enable: res.yieldEmailEnable,
      to: res.yieldEmailTo || '',
      from: res.yieldEmailFrom || '',
      smtpHost: res.yieldEmailSmtpHost || '',
      smtpPort: res.yieldEmailSmtpPort || 465,
      smtpUsername: res.yieldEmailSmtpUsername || '',
      smtpPassword: res.yieldEmailSmtpPassword || '',
      cronEnabled: res.yieldEmailCronEnabled === true,
      cronTimes: res.yieldEmailCronTimes || ''
    }
    formValue.value.updateBasicInfoOnStart = res.updateBasicInfoOnStart
    formValue.value.refreshInterval = res.refreshInterval
    // 加载AI配置
    formValue.value.openAI = {
      enable: res.openAiEnable,
      aiConfigs: res.aiConfigs || [],
      prompt: res.prompt,
      questionTemplate: res.questionTemplate ? res.questionTemplate : '{{stockName}}分析和总结',
      crawlTimeOut: res.crawlTimeOut,
      kDays: res.kDays,
      httpProxy:"",
      httpProxyEnabled:false,
    }
    formValue.value.browserPath = res.browserPath
    formValue.value.darkTheme = res.darkTheme
    formValue.value.minuteProviderMode = res.minuteProviderMode || 'public'
    formValue.value.minuteLongHistoryHintEnabled = res.minuteLongHistoryHintEnabled !== false
    formValue.value.akshareEnabled = res.akshareEnabled !== false
    formValue.value.sinaMinuteEnabled = res.sinaMinuteEnabled !== false
    formValue.value.tencentMinuteEnabled = res.tencentMinuteEnabled !== false
    formValue.value.akshareMinuteSourceMode = res.akshareMinuteSourceMode || 'auto'
    formValue.value.privateMinute = {
      enabled: res.privateMinuteEnabled === true,
      baseUrl: res.privateMinuteBaseUrl || '',
      apiKey: res.privateMinuteApiKey || '',
      timeoutSec: res.privateMinuteTimeoutSec || 60,
      minIntervalMs: Number.isFinite(res.privateMinuteMinIntervalMs) ? res.privateMinuteMinIntervalMs : 1200,
      proxyMode: res.privateMinuteProxyMode || 'disable',
      level: res.privateMinuteLevel || '1min',
    }
    formValue.value.enableFund = res.enableFund
    formValue.value.sponsorCode = res.sponsorCode
    formValue.value.httpProxy=res.httpProxy;
    formValue.value.httpProxyEnabled=res.httpProxyEnabled;
    formValue.value.forceNoProxyForFetch = res.forceNoProxyForFetch !== false;
    formValue.value.enableAgent = res.enableAgent;
    formValue.value.qgqpBId = res.qgqpBId;
    formValue.value.marketSummaryCronEnabled = res.marketSummaryCronEnabled !== false;
    formValue.value.marketSummaryCronTimes = res.marketSummaryCronTimes || '09:30,11:30,18:00';

  })

  GetPromptTemplates("", "").then(res => {
    promptTemplates.value = res
  })
  refreshEmailSendLogs()
})
onBeforeUnmount(() => {
  message.destroyAll()
})

function saveConfig() {
  persistConfig({showSuccess: true})
}

function buildConfigPayload() {
  return new data.SettingConfig({
    ID: formValue.value.ID,
    dingPushEnable: false,
    dingRobot: "",
    yieldEmailEnable: formValue.value.yieldEmail.enable,
    yieldEmailTo: formValue.value.yieldEmail.to,
    yieldEmailFrom: formValue.value.yieldEmail.from,
    yieldEmailSmtpHost: formValue.value.yieldEmail.smtpHost,
    yieldEmailSmtpPort: formValue.value.yieldEmail.smtpPort,
    yieldEmailSmtpUsername: formValue.value.yieldEmail.smtpUsername,
    yieldEmailSmtpPassword: formValue.value.yieldEmail.smtpPassword,
    yieldEmailCronEnabled: formValue.value.yieldEmail.cronEnabled,
    yieldEmailCronTimes: formValue.value.yieldEmail.cronTimes,
    localPushEnable: false,
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
    enableDanmu: false,
    browserPath: formValue.value.browserPath,
    enableNews: false,
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
    eastmoneyMinuteEnabled: true,
    akshareMinuteSourceMode: formValue.value.akshareMinuteSourceMode,
    enableFund: formValue.value.enableFund,
    enablePushNews: false,
    enableOnlyPushRedNews: false,
    sponsorCode: formValue.value.sponsorCode,
    httpProxy:formValue.value.httpProxy,
    httpProxyEnabled:formValue.value.httpProxyEnabled,
    forceNoProxyForFetch: formValue.value.forceNoProxyForFetch,
    enableAgent: formValue.value.enableAgent,
    qgqpBId: formValue.value.qgqpBId,
    marketSummaryCronEnabled: formValue.value.marketSummaryCronEnabled,
    marketSummaryCronTimes: formValue.value.marketSummaryCronTimes
  })
}

function parseYieldEmailRecipients(input) {
  return String(input || "")
      .replace(/[；;，]/g, ",")
      .split(",")
      .map(item => item.trim())
      .filter(Boolean)
}

function parseYieldEmailCronTimes(input) {
  return String(input || "")
      .replace(/[，；;]/g, ",")
      .replace(/\s+/g, "")
      .split(",")
      .map(item => item.trim())
      .filter(Boolean)
}

function isValidEmailText(email) {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(String(email || "").trim())
}

function isValidTimeText(hhmm) {
  return /^([01]\d|2[0-3]):([0-5]\d)$/.test(String(hhmm || "").trim())
}

function validateYieldEmailConfig() {
  if (!formValue.value.yieldEmail.enable) {
    return true
  }

  const recipients = parseYieldEmailRecipients(formValue.value.yieldEmail.to)
  if (recipients.length === 0) {
    message.error("请至少填写一个收件邮箱")
    return false
  }
  const invalidRecipients = recipients.filter(item => !isValidEmailText(item))
  if (invalidRecipients.length > 0) {
    message.error(`收件邮箱格式错误：${invalidRecipients.join(", ")}`)
    return false
  }

  if (formValue.value.yieldEmail.cronEnabled) {
    const times = parseYieldEmailCronTimes(formValue.value.yieldEmail.cronTimes)
    if (times.length === 0) {
      message.error("请至少填写一个定时发送时间，例如 09:30,15:05")
      return false
    }
    const invalidTimes = times.filter(item => !isValidTimeText(item))
    if (invalidTimes.length > 0) {
      message.error(`定时发送时间格式错误：${invalidTimes.join(", ")}（正确格式：HH:mm）`)
      return false
    }
  }
  return true
}

function validateMinuteSourceConfig() {
  if (formValue.value.minuteProviderMode === 'public') {
    if (!formValue.value.akshareEnabled && !formValue.value.sinaMinuteEnabled && !formValue.value.tencentMinuteEnabled) {
      message.error("公共分钟线模式下，至少保留一个公共数据源")
      return false
    }
    return true
  }

  if (!formValue.value.privateMinute.enabled) {
    message.error("私人分钟线模式下，请先启用私人分钟线来源")
    return false
  }
  if (!String(formValue.value.privateMinute.baseUrl || "").trim()) {
    message.error("请填写私人分钟线来源的调用 URL")
    return false
  }
  if (!String(formValue.value.privateMinute.apiKey || "").trim()) {
    message.error("请填写私人分钟线来源的 API Key")
    return false
  }
  return true
}

async function persistConfig(options = {}) {
  const showSuccess = options.showSuccess === true
  if (!validateYieldEmailConfig()) {
    return false
  }
  if (!validateMinuteSourceConfig()) {
    return false
  }

  const config = buildConfigPayload()
  const update = async () => {
    const res = await UpdateConfig(config)
    if (String(res || '').includes('失败')) {
      message.error(res)
      return false
    }
    if (showSuccess) {
      message.success(res)
    }
    EventsEmit("updateSettings", config);
    return true
  }

  return update()
}


async function sendYieldEmailTest() {
  if (yieldEmailTestSending.value) {
    return
  }
  yieldEmailTestSending.value = true
  try {
    const saved = await persistConfig()
    if (!saved) {
      return
    }
    const res = await SendYieldEmailTestMessage()
    if (String(res || '').includes('失败')) {
      message.error(res)
      await refreshEmailSendLogs(1)
      return
    }
    message.success(res)
    await refreshEmailSendLogs(1)
  } finally {
    yieldEmailTestSending.value = false
  }
}

async function sendYieldEmailCSVNowAction() {
  if (yieldEmailCsvSending.value) {
    return
  }
  yieldEmailCsvSending.value = true
  try {
    const saved = await persistConfig()
    if (!saved) {
      return
    }
    const res = await SendYieldEmailCSVNow()
    if (String(res || '').includes('失败')) {
      message.error(res)
      await refreshEmailSendLogs(1)
      return
    }
    message.success(res)
    await refreshEmailSendLogs(1)
  } finally {
    yieldEmailCsvSending.value = false
  }
}

async function sendLatestAIAnalysisReportNowAction() {
  if (yieldEmailAiReportSending.value) {
    return
  }
  yieldEmailAiReportSending.value = true
  try {
    const saved = await persistConfig()
    if (!saved) {
      return
    }
    const res = await SendLatestAIAnalysisReportNow()
    if (String(res || '').includes('失败')) {
      message.error(res)
      await refreshEmailSendLogs(1)
      return
    }
    message.success(res)
    await refreshEmailSendLogs(1)
  } finally {
    yieldEmailAiReportSending.value = false
  }
}

function formatDateTime(value) {
  if (!value) {
    return "-"
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return String(value)
  }
  const pad = (n) => String(n).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function formatSendType(value) {
  switch (String(value || "").trim()) {
    case "test":
      return "测试邮件"
    case "csv":
      return "收益率 CSV"
    case "manual_ai":
      return "手动 AI 报告"
    case "cron_ai":
      return "定时 AI 报告"
    default:
      return value || "-"
  }
}

function formatAttachmentText(item) {
  const count = Number(item?.attachmentCount || 0)
  if (count <= 0) {
    return "-"
  }
  const names = String(item?.attachmentNames || "").trim()
  if (!names) {
    return `${count} 个附件`
  }
  return `${names} (${count} 个)`
}

function formatReportText(item) {
  const name = String(item?.reportStockName || "").trim()
  const code = String(item?.reportStockCode || "").trim()
  if (name && code) {
    return `${name} [${code}]`
  }
  if (name || code) {
    return name || code
  }
  return "-"
}

async function refreshEmailSendLogs(pageNo = emailSendLogPage.value) {
  emailSendLogsLoading.value = true
  try {
    const page = await GetEmailSendLogList(new models.EmailSendLogQuery({
      page: pageNo,
      pageSize: 5
    }))
    emailSendLogs.value = Array.isArray(page?.list) ? page.list : []
    emailSendLogPage.value = Number(page?.page || pageNo || 1)
    emailSendLogTotalPages.value = Math.max(1, Number(page?.totalPages || 1))
    emailSendLogTotal.value = Number(page?.total || 0)
    if (emailSendLogPage.value > emailSendLogTotalPages.value) {
      await refreshEmailSendLogs(emailSendLogTotalPages.value)
    }
  } finally {
    emailSendLogsLoading.value = false
  }
}

function prevEmailSendLogPage() {
  if (emailSendLogPage.value <= 1 || emailSendLogsLoading.value) {
    return
  }
  refreshEmailSendLogs(emailSendLogPage.value - 1)
}

function nextEmailSendLogPage() {
  if (emailSendLogPage.value >= emailSendLogTotalPages.value || emailSendLogsLoading.value) {
    return
  }
  refreshEmailSendLogs(emailSendLogPage.value + 1)
}

function exportConfig() {
  ExportConfig().then(res => {
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
      formValue.value.ID = config.ID
      formValue.value.tushareToken = config.tushareToken
      formValue.value.yieldEmail = {
        enable: config.yieldEmailEnable,
        to: config.yieldEmailTo || '',
        from: config.yieldEmailFrom || '',
        smtpHost: config.yieldEmailSmtpHost || '',
        smtpPort: config.yieldEmailSmtpPort || 465,
        smtpUsername: config.yieldEmailSmtpUsername || '',
        smtpPassword: config.yieldEmailSmtpPassword || '',
        cronEnabled: config.yieldEmailCronEnabled === true,
        cronTimes: config.yieldEmailCronTimes || ''
      }
      formValue.value.updateBasicInfoOnStart = config.updateBasicInfoOnStart
      formValue.value.refreshInterval = config.refreshInterval
      // 导入AI配置
      formValue.value.openAI = {
        enable: config.openAiEnable,
        aiConfigs: config.aiConfigs || [],
        prompt: config.prompt,
        questionTemplate: config.questionTemplate,
        crawlTimeOut: config.crawlTimeOut,
        kDays: config.kDays
      }
      formValue.value.browserPath = config.browserPath
      formValue.value.darkTheme = config.darkTheme
      formValue.value.minuteProviderMode = config.minuteProviderMode || 'public'
      formValue.value.minuteLongHistoryHintEnabled = config.minuteLongHistoryHintEnabled !== false
      formValue.value.akshareEnabled = config.akshareEnabled !== false
      formValue.value.sinaMinuteEnabled = config.sinaMinuteEnabled !== false
      formValue.value.tencentMinuteEnabled = config.tencentMinuteEnabled !== false
      formValue.value.akshareMinuteSourceMode = config.akshareMinuteSourceMode || 'auto'
      formValue.value.privateMinute = {
        enabled: config.privateMinuteEnabled === true,
        baseUrl: config.privateMinuteBaseUrl || '',
        apiKey: config.privateMinuteApiKey || '',
        timeoutSec: config.privateMinuteTimeoutSec || 60,
        minIntervalMs: Number.isFinite(config.privateMinuteMinIntervalMs) ? config.privateMinuteMinIntervalMs : 1200,
        proxyMode: config.privateMinuteProxyMode || 'disable',
        level: config.privateMinuteLevel || '1min',
      }
      formValue.value.enableFund = config.enableFund
      formValue.value.marketSummaryCronEnabled = config.marketSummaryCronEnabled !== false
      formValue.value.marketSummaryCronTimes = config.marketSummaryCronTimes || '09:30,11:30,18:00'
      formValue.value.sponsorCode = config.sponsorCode
      formValue.value.httpProxy=config.httpProxy
      formValue.value.httpProxyEnabled=config.httpProxyEnabled
      formValue.value.enableAgent = config.enableAgent
      formValue.value.qgqpBId = config.qgqpBId
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
              <n-input type="text" placeholder="Tushare api token" v-model:value="formValue.tushareToken" clearable/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="启动时更新基础信息：" path="updateBasicInfoOnStart">
              <n-switch v-model:value="formValue.updateBasicInfoOnStart"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="数据刷新间隔：" path="refreshInterval">
              <n-input-number v-model:value="formValue.refreshInterval" placeholder="请输入数据刷新间隔(秒)">
                <template #suffix>秒</template>
              </n-input-number>
            </n-form-item-gi>
            <n-form-item-gi :span="6" label="暗黑主题：" path="darkTheme">
              <n-switch v-model:value="formValue.darkTheme"/>
            </n-form-item-gi>
            <n-form-item-gi :span="10" label="浏览器安装路径：" path="browserPath">
              <n-input type="text" placeholder="浏览器安装路径" v-model:value="formValue.browserPath" clearable/>
            </n-form-item-gi>
            <n-form-item-gi :span="3" label="AI智能体：" path="enableAgent">
              <n-switch v-model:value="formValue.enableAgent"/>
            </n-form-item-gi>
            <n-form-item-gi :span="11" label="东财唯一标识：" path="qgqpBId">
              <n-input type="text" placeholder="东财唯一标识" v-model:value="formValue.qgqpBId" clearable/>
            </n-form-item-gi>
          </n-grid>
        </n-card>

        <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => '分钟线数据源')" size="small">
          <n-grid :cols="24" :x-gap="24" style="text-align: left">
            <n-form-item-gi :span="24">
              <n-alert type="info" :show-icon="false">
                公共源更适合实时与短周期分钟线。若要覆盖更长时间的历史分钟线，请切换到私人分钟线来源并填写调用 URL 与 API Key。
              </n-alert>
            </n-form-item-gi>

            <n-form-item-gi :span="8" label="分钟线模式：" path="minuteProviderMode">
              <n-radio-group v-model:value="formValue.minuteProviderMode">
                <n-space>
                  <n-radio-button
                      v-for="item in minuteProviderModeOptions"
                      :key="item.value"
                      :value="item.value"
                  >
                    {{ item.label }}
                  </n-radio-button>
                </n-space>
              </n-radio-group>
            </n-form-item-gi>
            <n-form-item-gi :span="8" label="AKShare 来源偏好：" path="akshareMinuteSourceMode">
              <n-select
                  v-model:value="formValue.akshareMinuteSourceMode"
                  :options="akshareMinuteSourceOptions"
                  :disabled="!formValue.akshareEnabled"
              />
            </n-form-item-gi>

            <n-form-item-gi :span="4" label="AKShare：" path="akshareEnabled">
              <n-switch v-model:value="formValue.akshareEnabled"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="新浪分钟线：" path="sinaMinuteEnabled">
              <n-switch v-model:value="formValue.sinaMinuteEnabled"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="腾讯分钟线：" path="tencentMinuteEnabled">
              <n-switch v-model:value="formValue.tencentMinuteEnabled"/>
            </n-form-item-gi>
            <n-form-item-gi :span="24">
              <n-alert type="warning" :show-icon="false">
                私人分钟线来源不会在页面中展示具体服务商名称；这里只提供通用 URL 与 API Key 配置入口。
              </n-alert>
            </n-form-item-gi>

            <n-form-item-gi :span="4" label="启用私人来源：" path="privateMinute.enabled">
              <n-switch v-model:value="formValue.privateMinute.enabled"/>
            </n-form-item-gi>
            <n-form-item-gi :span="10" label="调用 URL：" path="privateMinute.baseUrl">
              <n-input
                  type="text"
                  placeholder="例如 https://example.com/api"
                  v-model:value="formValue.privateMinute.baseUrl"
                  clearable
              />
            </n-form-item-gi>
            <n-form-item-gi :span="10" label="API Key：" path="privateMinute.apiKey">
              <n-input
                  type="password"
                  placeholder="私人分钟线来源 API Key"
                  v-model:value="formValue.privateMinute.apiKey"
                  show-password-on="click"
                  clearable
              />
            </n-form-item-gi>
            <n-form-item-gi :span="6" label="超时(秒)：" path="privateMinute.timeoutSec">
              <n-input-number :min="1" v-model:value="formValue.privateMinute.timeoutSec"/>
            </n-form-item-gi>
            <n-form-item-gi :span="6" label="最小间隔(ms)：" path="privateMinute.minIntervalMs">
              <n-input-number :min="0" v-model:value="formValue.privateMinute.minIntervalMs"/>
            </n-form-item-gi>
            <n-form-item-gi :span="6" label="代理模式：" path="privateMinute.proxyMode">
              <n-select v-model:value="formValue.privateMinute.proxyMode" :options="privateMinuteProxyModeOptions"/>
            </n-form-item-gi>
            <n-form-item-gi :span="6" label="分钟级别：" path="privateMinute.level">
              <n-select v-model:value="formValue.privateMinute.level" :options="privateMinuteLevelOptions"/>
            </n-form-item-gi>
          </n-grid>
        </n-card>

        <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => '通知设置')" size="small">
          <n-grid :cols="24" :x-gap="24" style="text-align: left">
            <n-form-item-gi :span="4" label="邮箱推送收益率：" path="yieldEmail.enable">
              <n-switch v-model:value="formValue.yieldEmail.enable"/>
            </n-form-item-gi>

            <n-form-item-gi :span="24" v-if="formValue.yieldEmail.enable">
              <n-alert type="info" :show-icon="false">
                支持多个收件邮箱。现在可以单独立刻发送收益率 CSV，也可以单独立刻发送最新一篇 AI 分析报告；定时任务会在交易日按配置时间发送最新一篇 AI 分析报告。多个邮箱与多个时间都请用英文逗号分隔。
              </n-alert>
            </n-form-item-gi>
            <n-form-item-gi :span="12" v-if="formValue.yieldEmail.enable" label="收件邮箱：" path="yieldEmail.to">
              <n-input placeholder="多个收件邮箱用英文逗号分隔" v-model:value="formValue.yieldEmail.to" clearable/>
            </n-form-item-gi>
            <n-form-item-gi :span="12" v-if="formValue.yieldEmail.enable" label="发件邮箱：" path="yieldEmail.from">
              <n-input placeholder="用于 SMTP 登录的发件邮箱" v-model:value="formValue.yieldEmail.from" clearable/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" v-if="formValue.yieldEmail.enable" label="SMTP 主机：" path="yieldEmail.smtpHost">
              <n-input placeholder="可留空，按发件邮箱自动推断" v-model:value="formValue.yieldEmail.smtpHost" clearable/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" v-if="formValue.yieldEmail.enable" label="SMTP 端口：" path="yieldEmail.smtpPort">
              <n-input-number v-model:value="formValue.yieldEmail.smtpPort" :min="1" :max="65535"/>
            </n-form-item-gi>
            <n-form-item-gi :span="6" v-if="formValue.yieldEmail.enable" label="SMTP 用户名：" path="yieldEmail.smtpUsername">
              <n-input placeholder="可留空，默认使用发件邮箱" v-model:value="formValue.yieldEmail.smtpUsername" clearable/>
            </n-form-item-gi>
            <n-form-item-gi :span="6" v-if="formValue.yieldEmail.enable" label="SMTP 授权码：" path="yieldEmail.smtpPassword">
              <n-input type="password" placeholder="邮箱 SMTP 授权码/密码" v-model:value="formValue.yieldEmail.smtpPassword" show-password-on="click" clearable/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" v-if="formValue.yieldEmail.enable" label="交易日定时发送：" path="yieldEmail.cronEnabled">
              <n-switch v-model:value="formValue.yieldEmail.cronEnabled"/>
            </n-form-item-gi>
            <n-form-item-gi :span="20" v-if="formValue.yieldEmail.enable && formValue.yieldEmail.cronEnabled" label="发送时间：" path="yieldEmail.cronTimes">
              <n-input
                  placeholder="多个时间用英文逗号分隔，例如 09:30,15:05"
                  v-model:value="formValue.yieldEmail.cronTimes"
                  clearable
              />
            </n-form-item-gi>
            <n-form-item-gi :span="24" v-if="formValue.yieldEmail.enable">
              <n-space vertical>
                <n-space>
                  <n-button type="primary" :loading="yieldEmailTestSending" @click="sendYieldEmailTest">发送“你好”测试邮件</n-button>
                  <n-button type="success" :loading="yieldEmailCsvSending" @click="sendYieldEmailCSVNowAction">立刻发送收益率 CSV</n-button>
                  <n-button type="warning" :loading="yieldEmailAiReportSending" @click="sendLatestAIAnalysisReportNowAction">立刻发送 AI 分析报告</n-button>
                  <n-button tertiary @click="refreshEmailSendLogs" :loading="emailSendLogsLoading">刷新发送日志</n-button>
                </n-space>
                <n-text depth="3">收益率 CSV 会单独发送整张收益率表；AI 分析报告发送的是数据库里最新一篇 AI 分析报告；定时发送仅在交易日触发。</n-text>
              </n-space>
            </n-form-item-gi>
            <n-form-item-gi :span="24" v-if="formValue.yieldEmail.enable">
              <n-card size="small" title="最近邮件发送日志">
                <n-data-table
                    :loading="emailSendLogsLoading"
                    :bordered="false"
                    :single-line="false"
                    size="small"
                    :columns="[
                      { title: '触发时间', key: 'triggeredAt', width: 168, render: (row) => formatDateTime(row.triggeredAt || row.CreatedAt) },
                      { title: '类型', key: 'sendType', width: 120, render: (row) => formatSendType(row.sendType) },
                      { title: '状态', key: 'status', width: 90, render: (row) => row.status === 'success' ? h(NTag, { type: 'success', bordered: false }, () => '成功') : h(NTag, { type: 'error', bordered: false }, () => '失败') },
                      { title: '收件人', key: 'recipients', width: 220, ellipsis: { tooltip: true } },
                      { title: '主题', key: 'subject', width: 260, ellipsis: { tooltip: true } },
                      { title: '报告', key: 'report', width: 180, render: (row) => formatReportText(row) },
                      { title: '附件', key: 'attachmentNames', width: 220, render: (row) => formatAttachmentText(row), ellipsis: { tooltip: true } },
                      { title: '摘要', key: 'extraSummary', width: 220, ellipsis: { tooltip: true } },
                      { title: '错误信息', key: 'errorMessage', minWidth: 260, ellipsis: { tooltip: true }, render: (row) => row.errorMessage || '-' }
                    ]"
                    :data="emailSendLogs"
                    :pagination="false"
                />
                <n-flex justify="space-between" align="center" style="margin-top: 12px;">
                  <n-text depth="3">
                    第 {{ emailSendLogPage }} / {{ emailSendLogTotalPages }} 页，共 {{ emailSendLogTotal }} 条
                  </n-text>
                  <n-space>
                    <n-button size="small" @click="prevEmailSendLogPage" :disabled="emailSendLogPage <= 1 || emailSendLogsLoading">
                      上一页
                    </n-button>
                    <n-button size="small" @click="nextEmailSendLogPage" :disabled="emailSendLogPage >= emailSendLogTotalPages || emailSendLogsLoading">
                      下一页
                    </n-button>
                  </n-space>
                </n-flex>
              </n-card>
            </n-form-item-gi>
          </n-grid>
        </n-card>

        <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => 'AI设置')" size="small">
          <n-grid :cols="24" :x-gap="24" style="text-align: left;">
            <n-form-item-gi :span="24" label="AI诊股：" path="openAI.enable">
              <n-switch v-model:value="formValue.openAI.enable"/>
            </n-form-item-gi>

            <n-form-item-gi :span="6" v-if="formValue.openAI.enable" label="Crawler Timeout(秒)"
                            title="资讯采集超时时间(秒)" path="openAI.crawlTimeOut">
              <n-input-number min="30" step="1" v-model:value="formValue.openAI.crawlTimeOut"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" v-if="formValue.openAI.enable" title="天数越多消耗tokens越多"
                            label="日K线数据(天)" path="openAI.kDays">
              <n-input-number min="30" step="1" max="60" v-model:value="formValue.openAI.kDays"/>
            </n-form-item-gi>
            <n-form-item-gi :span="2" label="爬虫http代理" path="httpProxyEnabled">
              <n-switch v-model:value="formValue.httpProxyEnabled" :disabled="formValue.forceNoProxyForFetch"/>
            </n-form-item-gi>
            <n-form-item-gi :span="4" label="抓取强制直连" path="forceNoProxyForFetch">
              <n-switch v-model:value="formValue.forceNoProxyForFetch"/>
            </n-form-item-gi>
            <n-form-item-gi :span="8" v-if="formValue.httpProxyEnabled && !formValue.forceNoProxyForFetch" title="http代理地址"
                            label="http代理地址" path="httpProxy">
              <n-input type="text" placeholder="爬虫http代理地址" v-model:value="formValue.httpProxy" clearable/>
            </n-form-item-gi>
            <n-gi :span="24" v-if="formValue.forceNoProxyForFetch">
              <n-tag type="success" :bordered="false">已强制关闭所有信息抓取代理，网页抓取与接口抓取都会直连</n-tag>
            </n-gi>


            <n-gi :span="24" v-if="formValue.openAI.enable">
              <n-divider title-placement="left">Prompt 内容设置</n-divider>
            </n-gi>
            <n-form-item-gi :span="12" v-if="formValue.openAI.enable" label="模型系统 Prompt" path="openAI.prompt">
              <n-input v-model:value="formValue.openAI.prompt" type="textarea" :show-count="true"
                       placeholder="请输入系统prompt" :autosize="{ minRows: 4, maxRows: 8 }"/>
            </n-form-item-gi>
            <n-form-item-gi :span="12" v-if="formValue.openAI.enable" label="模型用户 Prompt"
                            path="openAI.questionTemplate">
              <n-input v-model:value="formValue.openAI.questionTemplate" type="textarea" :show-count="true"
                       placeholder="请输入用户prompt:例如{{stockName}}[{{stockCode}}]分析和总结"
                       :autosize="{ minRows: 4, maxRows: 8 }"/>
            </n-form-item-gi>

            <n-gi :span="24" v-if="formValue.openAI.enable">
              <n-divider title-placement="left">AI模型服务配置</n-divider>
            </n-gi>
            <n-gi :span="24" v-if="formValue.openAI.enable">
              <n-space vertical>
                <n-card v-for="(aiConfig, index) in formValue.openAI.aiConfigs" :key="index" :bordered="true"
                        size="small">
                  <template #header>
                    <n-flex justify="space-between" align="center">
                      <n-text depth="3">AI 配置 #{{ index + 1 }}</n-text>
                      <n-button type="error" size="tiny" ghost @click="removeAiConfig(index)">删除</n-button>
                    </n-flex>
                  </template>
                  <n-grid :cols="24" :x-gap="24">
                    <n-form-item-gi :span="24" hidden label="配置ID" :path="`openAI.aiConfigs[${index}].ID`">
                      <n-input type="text" placeholder="配置ID" v-model:value="aiConfig.ID" clearable/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="12" label="配置名称" :path="`openAI.aiConfigs[${index}].name`">
                      <n-input type="text" placeholder="配置名称" v-model:value="aiConfig.name" clearable/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="12" label="接口地址" :path="`openAI.aiConfigs[${index}].baseUrl`">
                      <n-input type="text" placeholder="AI接口地址" v-model:value="aiConfig.baseUrl" clearable/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="12" label="令牌(apiKey)" :path="`openAI.aiConfigs[${index}].apiKey`">
                      <n-input type="password" placeholder="apiKey" v-model:value="aiConfig.apiKey" clearable
                               show-password-on="click"/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="8" label="模型名称" :path="`openAI.aiConfigs[${index}].modelName`">
                      <n-input type="text" placeholder="AI模型名称" v-model:value="aiConfig.modelName" clearable/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="5" label="Temperature" :path="`openAI.aiConfigs[${index}].temperature`">
                      <n-input-number placeholder="temperature" v-model:value="aiConfig.temperature" :step="0.1"/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="5" label="MaxTokens" :path="`openAI.aiConfigs[${index}].maxTokens`">
                      <n-input-number placeholder="maxTokens" v-model:value="aiConfig.maxTokens"/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="5" label="Timeout(秒)" :path="`openAI.aiConfigs[${index}].timeOut`">
                      <n-input-number min="60" step="1" placeholder="超时(秒)" v-model:value="aiConfig.timeOut"/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="12" label="http代理" :path="`openAI.aiConfigs[${index}].httpProxyEnabled`">
                      <n-switch v-model:value="aiConfig.httpProxyEnabled"/>
                    </n-form-item-gi>
                    <n-form-item-gi :span="12" v-if="aiConfig.httpProxyEnabled" title="http代理地址" :path="`openAI.aiConfigs[${index}].httpProxy`">
                      <n-input type="text" placeholder="http代理地址" v-model:value="aiConfig.httpProxy" clearable/>
                    </n-form-item-gi>
                  </n-grid>
                </n-card>
                <n-button type="primary" dashed @click="addAiConfig" style="width: 100%;">+ 添加AI配置</n-button>
              </n-space>
            </n-gi>

            <n-gi :span="24">
              <n-divider/>
            </n-gi>

            <n-gi :span="24">
              <n-space vertical>
                <n-space justify="center">
                  <n-button type="warning" @click="managePrompts">管理提示词模板</n-button>
                  <n-button type="primary" strong @click="saveConfig">保存设置</n-button>
                  <n-button type="info" @click="exportConfig">导出配置</n-button>
                  <n-button type="error" @click="importConfig">导入配置</n-button>
                </n-space>

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
