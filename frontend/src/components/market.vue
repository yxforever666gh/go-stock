<script setup>
import { computed, defineAsyncComponent, h, onBeforeMount, onBeforeUnmount, ref } from 'vue'
import {
  GetAIResponseResult,
  GetConfig,
  GetIndustryRank,
  GetPromptTemplates,
  GetTelegraphList,
  GlobalStockIndexes,
  ReFleshTelegraphList,
  SaveAsMarkdown,
  SendMarketSummaryEmailNow,
  ShareAnalysis,
  SummaryStockNews,
  GetAiConfigs,
  GetVersionInfo,
  UpdateConfig,
} from '../services/app-api'
import { EventsOff, EventsOn } from '../../wailsjs/runtime'
import { PulseOutline } from '@vicons/ionicons5'
import { NAvatar, useMessage, useNotification } from 'naive-ui'
import { useRoute } from 'vue-router'

const MarketNewsTab = defineAsyncComponent(() => import('../market-tabs/MarketNewsTab.vue'))
const GlobalIndexesTab = defineAsyncComponent(() => import('../market-tabs/GlobalIndexesTab.vue'))
const MajorIndexesTab = defineAsyncComponent(() => import('../market-tabs/MajorIndexesTab.vue'))
const IndustryRankTab = defineAsyncComponent(() => import('../market-tabs/IndustryRankTab.vue'))
const MoneyFlowTab = defineAsyncComponent(() => import('../market-tabs/MoneyFlowTab.vue'))
const LongTigerTab = defineAsyncComponent(() => import('../market-tabs/LongTigerTab.vue'))
const StockResearchTab = defineAsyncComponent(() => import('../market-tabs/StockResearchTab.vue'))
const StockNoticeTab = defineAsyncComponent(() => import('../market-tabs/StockNoticeTab.vue'))
const IndustryResearchTab = defineAsyncComponent(() => import('../market-tabs/IndustryResearchTab.vue'))
const HotTopicsTab = defineAsyncComponent(() => import('../market-tabs/HotTopicsTab.vue'))
const SelectStockTab = defineAsyncComponent(() => import('../market-tabs/SelectStockTab.vue'))
const FavoriteSitesTab = defineAsyncComponent(() => import('../market-tabs/FavoriteSitesTab.vue'))
const MdPreview = defineAsyncComponent(() => import('md-editor-v3').then((mod) => mod.MdPreview))

const route = useRoute()
const icon = ref('')

const message = useMessage()
const notify = useNotification()
const panelHeight = ref(window.innerHeight - 240)

const telegraphList = ref([])
const sinaNewsList = ref([])
const foreignNewsList = ref([])
const globalStockIndexes = ref({})
const summaryModal = ref(false)
const openAiEnabled = ref(false)
const darkTheme = ref(false)
const httpProxyEnabled = ref(false)
const theme = computed(() => (darkTheme.value ? 'dark' : 'light'))
const aiSummary = ref('')
const aiSummaryTime = ref('')
const modelName = ref('')
const chatId = ref('')
const draftQuestion = ref('')
const lastSummaryQuestion = ref('')
const questionDirty = ref(false)
const aiConfigId = ref(null)
const sysPromptId = ref(null)
const loading = ref(false)
const summaryRunning = ref(false)
const summaryStatusText = ref('')
const summaryErrorMessage = ref('')
const summaryHadError = ref(false)
const summaryEmailSending = ref(false)
const aiConfigs = ref([])
const sysPromptOptions = ref([])
const userPromptOptions = ref([])
const promptTemplates = ref([])
const savedSystemPrompt = ref('')
const savedQuestionTemplate = ref('')
const industryRanks = ref([])
const sort = ref('0')
const nowTab = ref('市场快讯')
const indexInterval = ref(null)
const indexIndustryRank = ref(null)
const stockCode = ref('')
const enableTools = ref(true)
const thinkingMode = ref(true)
const summaryCronEnabled = ref(true)
const summaryCronTimes = ref('09:40,11:30,14:30')
const visitedTabs = ref([])

const defaultMarketSummaryQuestion = '总结和分析股票市场新闻中的投资机会，并推荐8-12只A股候选，其中最多4只作为可交易生产候选，并给出关键价位与交易计划'
const summaryOutputHint = '固定输出：市场主线 / 候选方向 / 风险提示 / 推荐结论 / 交易计划说明 / 推荐股票池 / 跳过复审（结构化表格）；默认推荐8-12只A股候选，最多4只可交易生产候选；严格核验不足时可少于8只甚至0只，不得降门槛或编造凑数'
const legacyMarketSummaryQuestion = '总结和分析股票市场新闻中的投资机会'
const legacyMarketSummaryQuestionWithTime = '请根据当前时间，总结和分析股票市场新闻中的投资机会'
const legacyMarketSummaryQuestion2A = '总结和分析股票市场新闻中的投资机会，并推荐2个A股，并给出关键价位与交易计划'
const legacyMarketSummaryQuestion3A = '总结和分析股票市场新闻中的投资机会，并推荐3只A股股票'
const legacyMarketSummaryQuestion3A2 = '总结和分析股票市场新闻中的投资机会，并推荐3个A股股票'
const legacyMarketSummaryQuestion3A3 = '总结和分析股票市场新闻中的投资机会，并推荐3只A股'
const legacyMarketSummaryQuestion3A4 = '总结和分析股票市场新闻中的投资机会，并推荐3个A股'

const marketTabComponents = {
  市场快讯: MarketNewsTab,
  全球股指: GlobalIndexesTab,
  重大指数: MajorIndexesTab,
  行业排名: IndustryRankTab,
  个股资金流向: MoneyFlowTab,
  龙虎榜: LongTigerTab,
  个股研报: StockResearchTab,
  公司公告: StockNoticeTab,
  行业研究: IndustryResearchTab,
  当前热门: HotTopicsTab,
  指标选股: SelectStockTab,
  名站优选: FavoriteSitesTab,
}

const showSummaryButton = computed(() => openAiEnabled.value && nowTab.value === '市场快讯')

function markTabVisited(name) {
  if (!name || visitedTabs.value.includes(name)) {
    return
  }
  visitedTabs.value = [...visitedTabs.value, name]
}

function shouldRenderTab(name) {
  return visitedTabs.value.includes(name)
}

function resolveTabComponent(name) {
  return marketTabComponents[name]
}

function stripMarketSummaryInstruction(q) {
  const text = (q || '').trim()
  if (!text) return ''
  const marker = '【市场资讯AI总结输出规范】'
  const index = text.indexOf(marker)
  if (index >= 0) {
    return text.slice(0, index).trim()
  }
  return text
}

function containsMarketSummaryPlaceholders(q) {
  const text = (q || '').trim()
  if (!text) return false
  return ['{{stockName}}', '{{stockCode}}', '{stockName}', '{stockCode}', 'stockName', 'stockCode'].some((item) => text.includes(item))
}

function hasExplicitMarketSummaryRecommendationCount(q) {
  const text = (q || '').trim()
  if (!text) return false
  return /(?:推荐|筛选|选出|挑选|输出|给出)\s*\d+\s*(?:(?:-|~|～|至|到|–|—)\s*\d+\s*)?(?:只(?:\s*(?:A\s*股|股票|标的|候选股))?|个\s*(?:A\s*股|股票|标的|候选股|股))/.test(text)
}

function normalizeMarketSummaryQuestion(q) {
  const raw = (q || '').trim()
  if (!raw) return defaultMarketSummaryQuestion
  if (containsMarketSummaryPlaceholders(raw)) return defaultMarketSummaryQuestion
  const text = stripMarketSummaryInstruction(raw)
  if (!text) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestionWithTime) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion2A) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion3A) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion3A2) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion3A3) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion3A4) return defaultMarketSummaryQuestion
  if (text === '市场资讯分析和总结') return defaultMarketSummaryQuestion
  if (text === '市场资讯分析') return defaultMarketSummaryQuestion
  if (text.startsWith(legacyMarketSummaryQuestionWithTime) && !hasExplicitMarketSummaryRecommendationCount(text) && !text.includes('买卖区间') && !text.includes('关键价位') && !text.includes('交易计划')) {
    return defaultMarketSummaryQuestion
  }
  return text
}

function resolveMarketSummaryQuestionFromConfig(q) {
  const text = (q || '').trim()
  if (!text) return defaultMarketSummaryQuestion
  return normalizeMarketSummaryQuestion(text)
}

function setDraftQuestion(value, markDirty = false) {
  draftQuestion.value = normalizeMarketSummaryQuestion(value)
  questionDirty.value = markDirty
}

function setLastSummaryQuestion(value) {
  lastSummaryQuestion.value = normalizeMarketSummaryQuestion(value)
}

function resetSummaryOutput() {
  aiSummary.value = ''
  aiSummaryTime.value = ''
  modelName.value = ''
  chatId.value = ''
  summaryErrorMessage.value = ''
  summaryHadError.value = false
}

function formatSummaryLatency(latencyMs) {
  const ms = Number(latencyMs || 0)
  if (!ms || ms < 0) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)}s`
}

function getSummaryToolLabel(tool) {
  switch (tool) {
    case 'market_summary':
      return 'AI总结任务'
    case 'phase3.discovery.fetch':
      return '抓取市场资讯'
    case 'phase3.discovery.model':
      return '筛选候选方向'
    case 'phase3.evidence.verify':
      return '核验候选股票'
    case 'phase3.generate':
      return '生成总结正文'
    default:
      return tool || 'AI总结'
  }
}

function syncPromptDefaults() {
  if (!draftQuestion.value || !draftQuestion.value.trim()) {
    setDraftQuestion(resolveMarketSummaryQuestionFromConfig(savedQuestionTemplate.value), false)
  }
  if (sysPromptId.value || !savedSystemPrompt.value || sysPromptOptions.value.length === 0) {
    return
  }
  const matched = sysPromptOptions.value.find((item) => (item.content || '').trim() === savedSystemPrompt.value.trim())
  if (matched) {
    sysPromptId.value = matched.ID
  }
}

function getIndex() {
  GlobalStockIndexes()
    .then((res) => {
      globalStockIndexes.value = res || {}
    })
    .catch(() => {
      globalStockIndexes.value = {}
    })
}

function changeIndustryRankSort() {
  sort.value = sort.value === '0' ? '1' : '0'
  industryRank()
}

function industryRank() {
  GetIndustryRank(sort.value, 150)
    .then((result) => {
      const safeResult = Array.isArray(result) ? result : []
      industryRanks.value = safeResult
    })
    .catch(() => {
      industryRanks.value = []
    })
}

async function submitMarketSummary(questionText) {
  if (summaryRunning.value) {
    message.warning('AI总结正在执行，请勿重复点击。')
    return
  }
  const normalizedQuestion = normalizeMarketSummaryQuestion(questionText)
  resetSummaryOutput()
  summaryModal.value = true
  loading.value = true
  summaryRunning.value = true
  summaryStatusText.value = '正在准备市场资讯...'
  summaryErrorMessage.value = ''
  summaryHadError.value = false
  setDraftQuestion(normalizedQuestion, false)
  setLastSummaryQuestion(normalizedQuestion)
  try {
    await SummaryStockNews(normalizedQuestion, aiConfigId.value, sysPromptId.value, enableTools.value, thinkingMode.value)
  } catch (err) {
    summaryRunning.value = false
    loading.value = false
    summaryStatusText.value = 'AI总结启动失败'
    message.error(`AI总结启动失败：${err}`)
  }
}

function reAiSummary() {
  submitMarketSummary(draftQuestion.value)
}

async function openSummaryModal() {
  if (summaryRunning.value) {
    summaryModal.value = true
    message.warning('AI总结正在执行，请勿重复点击。')
    return
  }
  summaryModal.value = true
  loading.value = true
  summaryStatusText.value = '已打开AI总结，可点击“再次总结”开始执行'
  try {
    const result = await GetAIResponseResult('市场资讯')
    resetSummaryOutput()
    if (result?.content) {
      aiSummary.value = result.content
      if (result.question) {
        const normalizedQuestion = normalizeMarketSummaryQuestion(result.question)
        setLastSummaryQuestion(normalizedQuestion)
        if (!questionDirty.value || !draftQuestion.value || !draftQuestion.value.trim()) {
          setDraftQuestion(normalizedQuestion, false)
        }
      }
      if (result.modelName) {
        modelName.value = result.modelName
      }
      if (result.chatId) {
        chatId.value = result.chatId
      }
      if (result.CreatedAt) {
        const date = new Date(result.CreatedAt)
        const year = date.getFullYear()
        const month = String(date.getMonth() + 1).padStart(2, '0')
        const day = String(date.getDate()).padStart(2, '0')
        const hours = String(date.getHours()).padStart(2, '0')
        const minutes = String(date.getMinutes()).padStart(2, '0')
        const seconds = String(date.getSeconds()).padStart(2, '0')
        aiSummaryTime.value = `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
      }
    }
  } catch (err) {
    summaryStatusText.value = 'AI总结界面已打开，可点击“再次总结”开始执行'
    message.error(`读取上次总结失败：${err}`)
  } finally {
    loading.value = false
  }
}

function parseSummaryCronTimes(input) {
  const text = (input || '').replace(/[，；;]/g, ',').replace(/\s+/g, '')
  const rows = text.split(',').map((item) => item.trim()).filter(Boolean)
  return Array.from(new Set(rows))
}

function isValidTimeText(hhmm) {
  return /^([01]\d|2[0-3]):([0-5]\d)$/.test(hhmm)
}

async function saveSummaryCronConfig() {
  const times = parseSummaryCronTimes(summaryCronTimes.value)
  if (summaryCronEnabled.value && times.length === 0) {
    message.error('请至少填写一个时间点，例如 09:40,11:30,14:30')
    return
  }
  const invalid = times.filter((item) => !isValidTimeText(item))
  if (invalid.length > 0) {
    message.error(`时间格式错误：${invalid.join(', ')}（正确格式：HH:mm）`)
    return
  }

  try {
    summaryEmailSending.value = false
    const cfg = await GetConfig()
    cfg.marketSummaryCronEnabled = summaryCronEnabled.value
    cfg.marketSummaryCronTimes = times.join(',')
    const res = await UpdateConfig(cfg)
    summaryCronTimes.value = cfg.marketSummaryCronTimes || '09:40,11:30,14:30'
    message.success(`AI总结定时已保存：${res}`)
  } catch (e) {
    message.error(`保存失败：${e}`)
  }
}

function updateTab(name) {
  nowTab.value = name
  markTabVisited(name)
}

async function copyToClipboard() {
  try {
    await navigator.clipboard.writeText(aiSummary.value)
    message.success('分析结果已复制到剪切板')
  } catch (err) {
    message.error(`复制失败: ${err}`)
  }
}

async function sendMarketSummaryEmailAction() {
  if (summaryEmailSending.value) {
    return
  }
  const summaryText = String(aiSummary.value || '').trim()
  const failureReason = String(summaryErrorMessage.value || '').trim()
  if (!summaryText && !failureReason) {
    message.warning('当前没有可发送的 AI 总结内容')
    return
  }
  summaryEmailSending.value = true
  try {
    const res = await SendMarketSummaryEmailNow(
      aiSummary.value || '',
      lastSummaryQuestion.value || draftQuestion.value || '',
      modelName.value || '',
      aiSummaryTime.value || '',
      failureReason,
    )
    if (String(res || '').includes('失败')) {
      message.error(res)
      return
    }
    message.success(res)
  } finally {
    summaryEmailSending.value = false
  }
}

function saveAsMarkdown() {
  SaveAsMarkdown('市场资讯', '市场资讯').then((result) => {
    message.success(result)
  })
}

function onQuestionTemplateChange(value) {
  draftQuestion.value = normalizeMarketSummaryQuestion(value)
  questionDirty.value = true
}

function onQuestionInput(value) {
  draftQuestion.value = value
  questionDirty.value = true
}

function share() {
  ShareAnalysis('市场资讯', '市场资讯').then((msg) => {
    notify.info({
      avatar: () =>
        h(NAvatar, {
          size: 'small',
          round: false,
          src: icon.value,
        }),
      title: '分享到社区',
      duration: 1000 * 30,
      content: () =>
        h(
          'div',
          {
            style: {
              'text-align': 'left',
              'font-size': '14px',
            },
          },
          { default: () => msg },
        ),
    })
  })
}

function ReFlesh(source) {
  ReFleshTelegraphList(source).then((res) => {
    if (source === '财联社电报') {
      telegraphList.value = res
    }
    if (source === '新浪财经') {
      sinaNewsList.value = res
    }
    if (source === '外媒') {
      foreignNewsList.value = res
    }
  })
}

function bindWindowResize() {
  window.onresize = () => {
    panelHeight.value = window.innerHeight - 240
  }
}

onBeforeMount(() => {
  const initialTab = String(route.query.name || '市场快讯')
  nowTab.value = initialTab
  markTabVisited(initialTab)
  stockCode.value = String(route.query.stockCode || '')
  bindWindowResize()

  GetVersionInfo().then((result) => {
    icon.value = result?.icon || ''
  })
  GetConfig().then((result) => {
    openAiEnabled.value = Boolean(result.openAiEnable)
    darkTheme.value = result.darkTheme
    httpProxyEnabled.value = result.httpProxyEnabled
    summaryCronEnabled.value = result.marketSummaryCronEnabled !== false
    summaryCronTimes.value = result.marketSummaryCronTimes || '09:40,11:30,14:30'
    savedSystemPrompt.value = result.prompt || ''
    savedQuestionTemplate.value = result.questionTemplate || ''
    setDraftQuestion(resolveMarketSummaryQuestionFromConfig(savedQuestionTemplate.value), false)
    syncPromptDefaults()
  })
  GetPromptTemplates('', '').then((res) => {
    promptTemplates.value = res
    sysPromptOptions.value = promptTemplates.value.filter((item) => item.type === '模型系统Prompt')
    userPromptOptions.value = promptTemplates.value.filter((item) => item.type === '模型用户Prompt')
    syncPromptDefaults()
  })
  GetAiConfigs().then((res) => {
    aiConfigs.value = res || []
    if (aiConfigs.value.length > 0) {
      aiConfigId.value = aiConfigs.value[0].ID
    }
  })
  GetTelegraphList('财联社电报').then((res) => {
    telegraphList.value = res
  })
  GetTelegraphList('新浪财经').then((res) => {
    sinaNewsList.value = res
  })
  GetTelegraphList('外媒').then((res) => {
    foreignNewsList.value = res
  })
  getIndex()
  industryRank()

  indexInterval.value = setInterval(() => {
    getIndex()
  }, 3000)
  indexIndustryRank.value = setInterval(() => {
    industryRank()
    ReFlesh('财联社电报')
    ReFlesh('新浪财经')
    ReFlesh('外媒')
  }, 1000 * 10)

  // Clean up any stale event listeners (defensive)
  EventsOff('changeMarketTab')
  EventsOff('newTelegraph')
  EventsOff('newSinaNews')
  EventsOff('tradingViewNews')
  EventsOff('summaryStockNews')
  EventsOff('summaryStockNewsToolStatus')

  // Register event listeners
  EventsOn('changeMarketTab', async (msg) => {
    updateTab(msg.name)
  })

  EventsOn('newTelegraph', (data) => {
    if (data != null) {
      for (let i = 0; i < data.length; i++) {
        telegraphList.value.pop()
      }
      telegraphList.value.unshift(...data)
    }
  })

  EventsOn('newSinaNews', (data) => {
    if (data != null) {
      for (let i = 0; i < data.length; i++) {
        sinaNewsList.value.pop()
      }
      sinaNewsList.value.unshift(...data)
    }
  })

  EventsOn('tradingViewNews', (data) => {
    if (data != null) {
      for (let i = 0; i < data.length; i++) {
        foreignNewsList.value.pop()
      }
      foreignNewsList.value.unshift(...data)
    }
  })

  EventsOn('summaryStockNews', async (msg) => {
    if (msg === 'DONE') {
      summaryRunning.value = false
      loading.value = false
      if (summaryHadError.value && !String(aiSummary.value || '').trim()) {
        summaryStatusText.value = 'AI分析失败'
        return
      }
      summaryStatusText.value = 'AI分析完成'
      message.info('AI分析完成！')
      message.destroyAll()
      return
    }

    const code = Number(msg?.code ?? 1)
    if (code === 0) {
      summaryRunning.value = false
      loading.value = false
      summaryHadError.value = true
      summaryStatusText.value = 'AI分析失败'
      summaryErrorMessage.value = msg?.content || 'AI总结执行失败'
      if (msg?.content) {
        message.error(msg.content)
      }
      return
    }
    if (msg.chatId) {
      chatId.value = msg.chatId
    }
    if (msg.question) {
      setLastSummaryQuestion(msg.question)
    }
    if (msg.content) {
      loading.value = false
      aiSummary.value += msg.content
    }
    if (msg.extraContent) {
      loading.value = false
      aiSummary.value += msg.extraContent
    }
    if (msg.model) {
      modelName.value = msg.model
    }
    if (msg.time) {
      aiSummaryTime.value = msg.time
    }
  })

  EventsOn('summaryStockNewsToolStatus', async (msg) => {
    if (!msg || !msg.status) {
      return
    }
    const toolLabel = getSummaryToolLabel(msg.tool)
    const latency = formatSummaryLatency(msg.latencyMs)
    if (msg.status === 'busy') {
      summaryRunning.value = false
      loading.value = false
      message.warning('AI总结正在执行，请勿重复点击。')
      summaryStatusText.value = 'AI总结任务仍在执行'
      return
    }
    if (msg.status === 'running') {
      summaryRunning.value = true
      summaryStatusText.value = `${toolLabel}中...`
      return
    }
    if (msg.status === 'success') {
      summaryStatusText.value = msg.tool === 'phase3.generate' ? '总结正文生成完成' : latency ? `${toolLabel}完成（${latency}）` : `${toolLabel}完成`
      return
    }
    if (msg.status === 'error') {
      const detail = msg.error ? `：${msg.error}` : ''
      summaryStatusText.value = `${toolLabel}失败`
      message.error(`工具 ${msg.tool || 'unknown'} 执行失败${detail}`)
    }
  })
})

onBeforeUnmount(() => {
  EventsOff('changeMarketTab')
  EventsOff('newTelegraph')
  EventsOff('newSinaNews')
  EventsOff('tradingViewNews')
  EventsOff('summaryStockNews')
  EventsOff('summaryStockNewsToolStatus')
  clearInterval(indexInterval.value)
  clearInterval(indexIndustryRank.value)
  window.onresize = null
})
</script>

<template>
  <n-card>
    <n-tabs type="line" animated @update-value="updateTab" :value="nowTab" style="--wails-draggable:no-drag">
      <n-tab-pane v-for="name in Object.keys(marketTabComponents)" :key="name" :name="name" :tab="name">
        <component
          :is="resolveTabComponent(name)"
          v-if="shouldRenderTab(name)"
          :dark-theme="darkTheme"
          :telegraph-list="telegraphList"
          :sina-news-list="sinaNewsList"
          :foreign-news-list="foreignNewsList"
          :global-stock-indexes="globalStockIndexes"
          :panel-height="panelHeight"
          :industry-ranks="industryRanks"
          :sort="sort"
          :stock-code="stockCode"
          @refresh="ReFlesh"
          @toggle-sort="changeIndustryRankSort"
        />
      </n-tab-pane>
    </n-tabs>
  </n-card>
  <n-modal
    v-model:show="summaryModal"
    transform-origin="center"
    preset="card"
    class="market-summary-modal"
    style="width: min(960px, calc(100vw - 32px));"
    :title="'AI市场资讯总结'"
  >
    <div class="summary-modal-body">
      <n-spin size="small" :show="loading" class="summary-preview-spin">
        <div class="summary-preview-shell">
          <MdPreview v-if="summaryModal" class="summary-preview" :modelValue="aiSummary" :theme="theme" />
        </div>
      </n-spin>
    </div>
    <template #footer>
      <n-flex justify="space-between">
        <n-text type="info">
          <n-tag v-if="summaryRunning" type="warning" round :bordered="false">运行中</n-tag>
          <n-tag v-else-if="summaryStatusText" type="info" round :bordered="false">{{ summaryStatusText }}</n-tag>
          <n-tag v-if="modelName" type="warning" round :title="chatId" :bordered="false">{{ modelName }}</n-tag>
          <span v-if="aiSummaryTime">{{ aiSummaryTime }}</span>
        </n-text>
        <n-text type="error">*AI分析结果仅供参考，请以实际行情为准。投资需谨慎，风险自担。</n-text>
      </n-flex>
    </template>
    <template #action>
      <n-flex justify="space-between" align="center" style="margin-bottom: 10px">
        <n-flex align="center">
          <n-text depth="2">AI总结定时</n-text>
          <n-switch v-model:value="summaryCronEnabled" :round="false">
            <template #checked>开启</template>
            <template #unchecked>关闭</template>
          </n-switch>
        </n-flex>
        <n-input
          v-model:value="summaryCronTimes"
          :disabled="!summaryCronEnabled"
          placeholder="请输入时间，逗号分隔，例如 09:40,11:30,14:30"
          style="width: 420px;text-align:left;"
        />
        <n-button size="tiny" type="primary" @click="saveSummaryCronConfig">
          保存定时
        </n-button>
      </n-flex>
      <n-flex v-if="summaryStatusText" justify="left" style="margin-bottom: 10px">
        <n-text depth="2">{{ summaryStatusText }}</n-text>
      </n-flex>
      <n-flex justify="left" style="margin-bottom: 10px">
        <n-switch v-model:value="enableTools" :round="false">
          <template #checked>启用AI函数工具调用</template>
          <template #unchecked>不启用AI函数工具调用</template>
        </n-switch>
        <n-switch v-model:value="thinkingMode" :round="false">
          <template #checked>启用思考模式</template>
          <template #unchecked>不启用思考模式</template>
        </n-switch>
        <n-gradient-text type="error" style="margin-left: 10px">*AI函数工具调用可以增强AI获取数据的能力,但会消耗更多tokens。</n-gradient-text>
        <n-tag :bordered="false" type="info">{{ summaryOutputHint }}</n-tag>
      </n-flex>
      <n-flex justify="space-between" style="margin-bottom: 10px">
        <n-select
          v-model:value="aiConfigId"
          style="width: 32%"
          label-field="name"
          value-field="ID"
          :disabled="summaryRunning"
          :options="aiConfigs"
          placeholder="请选择AI模型服务配置"
        />
        <n-select
          v-model:value="sysPromptId"
          style="width: 32%"
          label-field="name"
          value-field="ID"
          :disabled="summaryRunning"
          :options="sysPromptOptions"
          placeholder="请选择系统提示词"
        />
        <n-select
          v-model:value="draftQuestion"
          style="width: 32%"
          label-field="name"
          value-field="content"
          :disabled="summaryRunning"
          :options="userPromptOptions"
          placeholder="请选择用户提示词"
          @update:value="onQuestionTemplateChange"
        />
      </n-flex>
      <n-flex justify="right">
        <n-input
          v-model:value="draftQuestion"
          style="text-align: left"
          clearable
          :disabled="summaryRunning"
          @update:value="onQuestionInput"
          type="textarea"
          :show-count="true"
          placeholder="请输入您的问题:例如 总结和分析股票市场新闻中的投资机会，并推荐8-12只A股候选，其中最多4只作为可交易生产候选，并给出买入区间、止盈区间、止损位与失效条件；严格核验不足时可少于8只甚至0只，不得降门槛或编造凑数"
          :autosize="{ minRows: 2, maxRows: 5 }"
        />
        <n-button size="tiny" type="warning" :loading="summaryRunning" :disabled="summaryRunning" @click="reAiSummary">再次总结</n-button>
        <n-button
          size="tiny"
          type="primary"
          :loading="summaryEmailSending"
          :disabled="summaryRunning || summaryEmailSending"
          @click="sendMarketSummaryEmailAction"
        >
          发送邮件
        </n-button>
        <n-button size="tiny" type="success" @click="copyToClipboard">复制到剪切板</n-button>
        <n-button size="tiny" type="primary" @click="saveAsMarkdown">保存为Markdown文件</n-button>
        <n-button size="tiny" type="error" @click="share">分享到项目社区</n-button>
      </n-flex>
    </template>
  </n-modal>

  <div v-if="showSummaryButton" style="position: fixed;bottom: 18px;right:25px;z-index: 10;">
    <n-input-group>
      <n-button type="primary" :loading="summaryRunning" @click="openSummaryModal">
        <n-icon :component="PulseOutline" /> &nbsp;{{ summaryRunning ? 'AI总结运行中' : 'AI总结' }}
      </n-button>
    </n-input-group>
  </div>
</template>

<style>
.summary-modal-body {
  flex: 1 1 auto;
  min-height: 0;
  max-height: min(52vh, 560px);
  overflow: hidden;
}

.summary-preview-spin {
  display: block;
  height: 100%;
}

.summary-preview-shell {
  height: 100%;
  max-height: min(52vh, 560px);
  overflow: auto;
  padding-right: 8px;
  box-sizing: border-box;
}

.summary-preview {
  text-align: left;
  min-height: 100%;
}

.summary-preview-shell :deep(.md-editor-preview) {
  min-height: 100%;
}

.summary-preview-shell :deep(.md-editor-preview-wrapper) {
  overflow: visible;
}

.market-summary-modal {
  max-height: calc(100vh - 24px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.market-summary-modal .n-card-header,
.market-summary-modal .n-card__footer {
  flex: 0 0 auto;
}

.market-summary-modal .n-card__content {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}

.market-summary-modal .n-card__action {
  flex: 0 1 auto;
  min-height: 0;
  max-height: min(32vh, 320px);
  overflow: auto;
}
</style>
