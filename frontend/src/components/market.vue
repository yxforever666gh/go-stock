<script setup>
import * as echarts from "echarts";
import {computed, h, onBeforeMount, onBeforeUnmount, onMounted,onUnmounted, ref} from 'vue'
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
} from "../services/app-api";
import {EventsOff, EventsOn} from "../../wailsjs/runtime";
import NewsList from "./newsList.vue";
import KLineChart from "./KLineChart.vue";
import { CaretDown, CaretUp, PulseOutline,} from "@vicons/ionicons5";
import {NAvatar, NButton, NFlex, NText, useMessage, useNotification} from "naive-ui";
import {MdPreview} from "md-editor-v3";
import {useRoute} from 'vue-router'
import RankTable from "./rankTable.vue";
import IndustryMoneyRank from "./industryMoneyRank.vue";
import StockResearchReportList from "./StockResearchReportList.vue";
import StockNoticeList from "./StockNoticeList.vue";
import LongTigerRankList from "./LongTigerRankList.vue";
import IndustryResearchReportList from "./IndustryResearchReportList.vue";
import HotStockList from "./HotStockList.vue";
import HotEvents from "./HotEvents.vue";
import HotTopics from "./HotTopics.vue";
import InvestCalendarTimeLine from "./InvestCalendarTimeLine.vue";
import ClsCalendarTimeLine from "./ClsCalendarTimeLine.vue";
import SelectStock from "./SelectStock.vue";
import Stockhotmap from "./stockhotmap.vue";

const route = useRoute()
const icon = ref('');

const message = useMessage()
const notify = useNotification()
const panelHeight = ref(window.innerHeight - 240)

const telegraphList = ref([])
const sinaNewsList = ref([])
const foreignNewsList = ref([])
const common = ref([])
const america = ref([])
const europe = ref([])
const asia = ref([])
const other = ref([])
const globalStockIndexes = ref(null)
const summaryModal = ref(false)
const summaryBTN = ref(true)
const darkTheme = ref(false)
const httpProxyEnabled = ref(false)
const theme = computed(() => {
  return darkTheme ? 'dark' : 'light'
})
const aiSummary = ref(``)
const aiSummaryTime = ref("")
const modelName = ref("")
const chatId = ref("")
const draftQuestion = ref(``)
const lastSummaryQuestion = ref(``)
const questionDirty = ref(false)
const aiConfigId = ref(null)
const sysPromptId = ref(null)
const loading = ref(false)
const summaryRunning = ref(false)
const summaryStatusText = ref("")
const summaryErrorMessage = ref("")
const summaryHadError = ref(false)
const summaryEmailSending = ref(false)
const aiConfigs = ref([])
const sysPromptOptions = ref([])
const userPromptOptions = ref([])
const promptTemplates = ref([])
const savedSystemPrompt = ref('')
const savedQuestionTemplate = ref('')
const industryRanks = ref([])
const sort = ref("0")
const nowTab = ref("市场快讯")
const indexInterval = ref(null)
const indexIndustryRank = ref(null)
const stockCode= ref('')
const enableTools= ref(true)
const thinkingMode = ref(true)
const summaryCronEnabled = ref(true)
const summaryCronTimes = ref("09:30,11:30,18:00")
const summaryCronSaving = ref(false)
const treemapRef = ref(null);
let treemapchart =null;

const defaultMarketSummaryQuestion = "总结和分析股票市场新闻中的投资机会，并推荐3个A股，并给出关键价位与交易计划"
const summaryOutputHint = "固定输出：市场主线 / 候选方向 / 风险提示 / 推荐结论 / 交易计划说明 / 推荐股票池 / 跳过复审（结构化表格）"
const legacyMarketSummaryQuestion = "总结和分析股票市场新闻中的投资机会"
const legacyMarketSummaryQuestionWithTime = "请根据当前时间，总结和分析股票市场新闻中的投资机会"
const legacyMarketSummaryQuestion3A = "总结和分析股票市场新闻中的投资机会，并推荐3只A股股票"
const legacyMarketSummaryQuestion3A2 = "总结和分析股票市场新闻中的投资机会，并推荐3个A股股票"

function stripMarketSummaryInstruction(q) {
  const text = (q || "").trim()
  if (!text) return ""
  const marker = "【市场资讯AI总结输出规范】"
  const index = text.indexOf(marker)
  if (index >= 0) {
    return text.slice(0, index).trim()
  }
  return text
}

function containsMarketSummaryPlaceholders(q) {
  const text = (q || "").trim()
  if (!text) return false
  return ["{{stockName}}", "{{stockCode}}", "{stockName}", "{stockCode}", "stockName", "stockCode"].some(item => text.includes(item))
}

function normalizeMarketSummaryQuestion(q) {
  const raw = (q || "").trim()
  if (!raw) return defaultMarketSummaryQuestion
  if (containsMarketSummaryPlaceholders(raw)) return defaultMarketSummaryQuestion
  const text = stripMarketSummaryInstruction(raw)
  if (!text) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestionWithTime) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion3A) return defaultMarketSummaryQuestion
  if (text === legacyMarketSummaryQuestion3A2) return defaultMarketSummaryQuestion
  if (text === "市场资讯分析和总结") return defaultMarketSummaryQuestion
  if (text === "市场资讯分析") return defaultMarketSummaryQuestion
  if (text.startsWith(legacyMarketSummaryQuestionWithTime) && !text.includes("买卖区间") && !text.includes("关键价位") && !text.includes("交易计划")) return defaultMarketSummaryQuestion
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
  aiSummary.value = ""
  aiSummaryTime.value = ""
  modelName.value = ""
  chatId.value = ""
  summaryErrorMessage.value = ""
  summaryHadError.value = false
}

function formatSummaryLatency(latencyMs) {
  const ms = Number(latencyMs || 0)
  if (!ms || ms < 0) return ""
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)}s`
}

function getSummaryToolLabel(tool) {
  switch (tool) {
    case "market_summary":
      return "AI总结任务"
    case "phase3.discovery.fetch":
      return "抓取市场资讯"
    case "phase3.discovery.model":
      return "筛选候选方向"
    case "phase3.evidence.verify":
      return "核验候选股票"
    case "phase3.generate":
      return "生成总结正文"
    default:
      return tool || "AI总结"
  }
}

function syncPromptDefaults() {
  if (!draftQuestion.value || !draftQuestion.value.trim()) {
    setDraftQuestion(resolveMarketSummaryQuestionFromConfig(savedQuestionTemplate.value), false)
  }
  if (sysPromptId.value || !savedSystemPrompt.value || sysPromptOptions.value.length === 0) {
    return
  }
  const matched = sysPromptOptions.value.find(item => (item.content || '').trim() === savedSystemPrompt.value.trim())
  if (matched) {
    sysPromptId.value = matched.ID
  }
}

function getIndex() {
  GlobalStockIndexes().then((res) => {
    const safeRes = res || {}
    globalStockIndexes.value = safeRes
    common.value = safeRes["common"] || []
    america.value = safeRes["america"] || []
    europe.value = safeRes["europe"] || []
    asia.value = safeRes["asia"] || []
    other.value = safeRes["other"] || []
  }).catch(() => {
    globalStockIndexes.value = {}
    common.value = []
    america.value = []
    europe.value = []
    asia.value = []
    other.value = []
  })
}

onBeforeMount(() => {
  nowTab.value = route.query.name
  stockCode.value = route.query.stockCode
  GetVersionInfo().then(result => {
    icon.value = result?.icon || ''
  })
  GetConfig().then(result => {
    summaryBTN.value = result.openAiEnable
    darkTheme.value = result.darkTheme
    httpProxyEnabled.value = result.httpProxyEnabled
    summaryCronEnabled.value = result.marketSummaryCronEnabled !== false
    summaryCronTimes.value = result.marketSummaryCronTimes || "09:30,11:30,18:00"
    savedSystemPrompt.value = result.prompt || ''
    savedQuestionTemplate.value = result.questionTemplate || ''
    setDraftQuestion(resolveMarketSummaryQuestionFromConfig(savedQuestionTemplate.value), false)
    syncPromptDefaults()
  })
  GetPromptTemplates("", "").then(res => {
    promptTemplates.value = res
    sysPromptOptions.value = promptTemplates.value.filter(item => item.type === '模型系统Prompt')
    userPromptOptions.value = promptTemplates.value.filter(item => item.type === '模型用户Prompt')
    syncPromptDefaults()
  })

  GetAiConfigs().then(res=>{
    aiConfigs.value = res
    aiConfigId.value = res[0].ID
  })
  GetTelegraphList("财联社电报").then((res) => {
    telegraphList.value = res
  })
  GetTelegraphList("新浪财经").then((res) => {
    sinaNewsList.value = res
  })
  GetTelegraphList("外媒").then((res) => {
    foreignNewsList.value = res
  })
  getIndex();
  industryRank();
  indexInterval.value = setInterval(() => {
    getIndex()
  }, 3000)

  indexIndustryRank.value = setInterval(() => {
    industryRank()
    ReFlesh("财联社电报")
    ReFlesh("新浪财经")
    ReFlesh("外媒")
  }, 1000 * 10)


})
onMounted(() => {
})


onBeforeUnmount(() => {
  EventsOff("changeMarketTab")
  EventsOff("newTelegraph")
  EventsOff("newSinaNews")
  EventsOff("summaryStockNews")
  EventsOff("summaryStockNewsToolStatus")
  clearInterval(indexInterval.value)
  clearInterval(indexIndustryRank.value)
})

onUnmounted(() => {

});
EventsOn("changeMarketTab", async (msg) => {
  //message.info(msg.name)
  console.log(msg.name)
  updateTab(msg.name)
})

EventsOn("newTelegraph", (data) => {
  if (data!=null) {
    for (let i = 0; i < data.length; i++) {
      telegraphList.value.pop()
    }
    telegraphList.value.unshift(...data)
  }
})
EventsOn("newSinaNews", (data) => {
  if (data!=null) {
  for (let i = 0; i < data.length; i++) {
    sinaNewsList.value.pop()
  }
  sinaNewsList.value.unshift(...data)
  }
})
EventsOn("tradingViewNews", (data) => {
  if (data!=null) {
    for (let i = 0; i < data.length; i++) {
      foreignNewsList.value.pop()
    }
    foreignNewsList.value.unshift(...data)
  }
})

//获取页面高度
window.onresize = () => {
  panelHeight.value = window.innerHeight - 240
}

function getAreaName(code) {
  switch (code) {
    case "america":
      return "美洲"
    case "europe":
      return "欧洲"
    case "asia":
      return "亚洲"
    case "common":
      return "常用"
    case "other":
      return "其他"
  }
}

function changeIndustryRankSort() {
  if (sort.value === "0") {
    sort.value = "1"
  } else {
    sort.value = "0"
  }
  industryRank()
}

function industryRank() {

  GetIndustryRank(sort.value, 150).then(result => {
    const safeResult = Array.isArray(result) ? result : []
    if (safeResult.length > 0) {
      //console.log(result)
      industryRanks.value = safeResult
    } else {
      industryRanks.value = []
    }
  }).catch(() => {
    industryRanks.value = []
  })
}

async function submitMarketSummary(questionText) {
  if (summaryRunning.value) {
    message.warning("AI总结正在执行，请勿重复点击。")
    return
  }
  const normalizedQuestion = normalizeMarketSummaryQuestion(questionText)
  resetSummaryOutput()
  summaryModal.value = true
  loading.value = true
  summaryRunning.value = true
  summaryStatusText.value = "正在准备市场资讯..."
  summaryErrorMessage.value = ""
  summaryHadError.value = false
  setDraftQuestion(normalizedQuestion, false)
  setLastSummaryQuestion(normalizedQuestion)
  try {
    await SummaryStockNews(normalizedQuestion, aiConfigId.value, sysPromptId.value, enableTools.value, thinkingMode.value)
  } catch (err) {
    summaryRunning.value = false
    loading.value = false
    summaryStatusText.value = "AI总结启动失败"
    message.error(`AI总结启动失败：${err}`)
  }
}

function reAiSummary() {
  submitMarketSummary(draftQuestion.value)
}

async function openSummaryModal() {
  if (summaryRunning.value) {
    summaryModal.value = true
    message.warning("AI总结正在执行，请勿重复点击。")
    return
  }
  summaryModal.value = true
  loading.value = true
  summaryStatusText.value = "已打开AI总结，可点击“再次总结”开始执行"
  try {
    const result = await GetAIResponseResult("市场资讯")
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
    summaryStatusText.value = "AI总结界面已打开，可点击“再次总结”开始执行"
    message.error(`读取上次总结失败：${err}`)
  } finally {
    loading.value = false
  }
}

function parseSummaryCronTimes(input) {
  const text = (input || "")
      .replace(/[，；;]/g, ",")
      .replace(/\s+/g, "")
  const rows = text.split(",").map(item => item.trim()).filter(Boolean)
  const uniq = Array.from(new Set(rows))
  return uniq
}

function isValidTimeText(hhmm) {
  return /^([01]\d|2[0-3]):([0-5]\d)$/.test(hhmm)
}

async function saveSummaryCronConfig() {
  const times = parseSummaryCronTimes(summaryCronTimes.value)
  if (summaryCronEnabled.value && times.length === 0) {
    message.error("请至少填写一个时间点，例如 09:30,11:30,18:00")
    return
  }
  const invalid = times.filter(item => !isValidTimeText(item))
  if (invalid.length > 0) {
    message.error(`时间格式错误：${invalid.join(", ")}（正确格式：HH:mm）`)
    return
  }

  summaryCronSaving.value = true
  try {
    const cfg = await GetConfig()
    cfg.marketSummaryCronEnabled = summaryCronEnabled.value
    cfg.marketSummaryCronTimes = times.join(",")
    const res = await UpdateConfig(cfg)
    summaryCronTimes.value = cfg.marketSummaryCronTimes || "09:30,11:30,18:00"
    message.success(`AI总结定时已保存：${res}`)
  } catch (e) {
    message.error(`保存失败：${e}`)
  } finally {
    summaryCronSaving.value = false
  }
}

function updateTab(name) {
  summaryBTN.value = (name === "市场快讯");
  nowTab.value = name
}

EventsOn("summaryStockNews", async (msg) => {
  if (msg === "DONE") {
    summaryRunning.value = false
    loading.value = false
    if (summaryHadError.value && !String(aiSummary.value || "").trim()) {
      summaryStatusText.value = "AI分析失败"
      return
    }
    summaryStatusText.value = "AI分析完成"
    message.info("AI分析完成！")
    message.destroyAll()
  } else {
    const code = Number(msg?.code ?? 1)
    if (code === 0) {
      summaryRunning.value = false
      loading.value = false
      summaryHadError.value = true
      summaryStatusText.value = "AI分析失败"
      summaryErrorMessage.value = msg?.content || "AI总结执行失败"
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
      aiSummary.value = aiSummary.value + msg.content
    }
    if (msg.extraContent) {
      loading.value = false
      aiSummary.value = aiSummary.value + msg.extraContent
    }
    if (msg.model) {
      modelName.value = msg.model
    }
    if (msg.time) {
      aiSummaryTime.value = msg.time
    }
  }
})

EventsOn("summaryStockNewsToolStatus", async (msg) => {
  if (!msg || !msg.status) {
    return
  }
  const toolLabel = getSummaryToolLabel(msg.tool)
  const latency = formatSummaryLatency(msg.latencyMs)
  if (msg.status === "busy") {
    summaryRunning.value = false
    loading.value = false
    message.warning("AI总结正在执行，请勿重复点击。")
    summaryStatusText.value = "AI总结任务仍在执行"
    return
  }
  if (msg.status === "running") {
    summaryRunning.value = true
    summaryStatusText.value = `${toolLabel}中...`
    return
  }
  if (msg.status === "success") {
    if (msg.tool === "phase3.generate") {
      summaryStatusText.value = "总结正文生成完成"
    } else {
      summaryStatusText.value = latency ? `${toolLabel}完成（${latency}）` : `${toolLabel}完成`
    }
    return
  }
  if (msg && msg.status === "error") {
    const detail = msg.error ? `：${msg.error}` : ""
    summaryStatusText.value = `${toolLabel}失败`
    message.error(`工具 ${msg.tool || "unknown"} 执行失败${detail}`)
  }
})

async function copyToClipboard() {
  try {
    await navigator.clipboard.writeText(aiSummary.value);
    message.success('分析结果已复制到剪切板');
  } catch (err) {
    message.error('复制失败: ' + err);
  }
}

async function sendMarketSummaryEmailAction() {
  if (summaryEmailSending.value) {
    return
  }
  const summaryText = String(aiSummary.value || "").trim()
  const failureReason = String(summaryErrorMessage.value || "").trim()
  if (!summaryText && !failureReason) {
    message.warning("当前没有可发送的 AI 总结内容")
    return
  }
  summaryEmailSending.value = true
  try {
    const res = await SendMarketSummaryEmailNow(
        aiSummary.value || "",
        lastSummaryQuestion.value || draftQuestion.value || "",
        modelName.value || "",
        aiSummaryTime.value || "",
        failureReason
    )
    if (String(res || "").includes("失败")) {
      message.error(res)
      return
    }
    message.success(res)
  } finally {
    summaryEmailSending.value = false
  }
}

function saveAsMarkdown() {
  SaveAsMarkdown('市场资讯', '市场资讯').then(result => {
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
  ShareAnalysis('市场资讯', '市场资讯').then(msg => {
    //message.info(msg)
    notify.info({
      avatar: () =>
          h(NAvatar, {
            size: 'small',
            round: false,
            src: icon.value
          }),
      title: '分享到社区',
      duration: 1000 * 30,
      content: () => {
        return h('div', {
          style: {
            'text-align': 'left',
            'font-size': '14px',
          }
        }, {default: () => msg})
      },
    })
  })
}

function ReFlesh(source) {
  //console.log("ReFlesh:", source)
  ReFleshTelegraphList(source).then(res => {
    if (source === "财联社电报") {
      telegraphList.value = res
    }
    if (source === "新浪财经") {
      sinaNewsList.value = res
    }
    if (source === "外媒") {
      foreignNewsList.value = res
    }
  })
}
</script>

<template>
  <n-card>
    <n-tabs type="line" animated @update-value="updateTab" :value="nowTab" style="--wails-draggable:no-drag">
      <n-tab-pane name="市场快讯" tab="市场快讯">
        <n-grid :cols="1" :y-gap="0">
          <n-gi>
            <AnalyzeMartket :dark-theme="darkTheme" :chart-height="300" :kDays="1" :name="'最近24小时热词'" />
          </n-gi>
          <n-gi>
            <n-grid :cols="foreignNewsList.length?3:2" :y-gap="0">
              <n-gi>
                <news-list :newsList="telegraphList" :header-title="'财联社电报'" @update:message="ReFlesh"></news-list>
              </n-gi>
              <n-gi>
                <news-list :newsList="sinaNewsList" :header-title="'新浪财经'" @update:message="ReFlesh"></news-list>
              </n-gi>
              <n-gi v-if="foreignNewsList.length>0">
                <news-list :newsList="foreignNewsList" :header-title="'外媒'" @update:message="ReFlesh"></news-list>
              </n-gi>

            </n-grid>
          </n-gi>
        </n-grid>

      </n-tab-pane>
      <n-tab-pane name="全球股指" tab="全球股指">
        <n-tabs type="segment" animated>
          <n-tab-pane name="全球指数" tab="全球指数">
            <n-grid :cols="5" :y-gap="0">
              <n-gi v-for="(val, key) in globalStockIndexes" :key="key">
                <n-list bordered>
                  <template #header>
                    {{ getAreaName(key) }}
                  </template>
                  <n-list-item v-for="item in val" :key="item.code">
                    <n-grid :cols="3" :y-gap="0">
                      <n-gi>

                        <n-text :type="item.zdf>0?'error':'success'">
                          <n-image :src="item.img" width="20"/> &nbsp;{{ item.name }}
                        </n-text>
                      </n-gi>
                      <n-gi>
                        <n-text :type="item.zdf>0?'error':'success'">{{ item.zxj }}</n-text>&nbsp;
                        <n-text :type="item.zdf>0?'error':'success'">
                          <n-number-animation :precision="2" :from="0" :to="item.zdf"/>
                          %
                        </n-text>

                      </n-gi>
                      <n-gi>
                        <n-text :type="item.state === 'open' ? 'success' : 'warning'">{{
                            item.state === 'open' ? '开市' : '休市'
                          }}
                        </n-text>
                      </n-gi>
                    </n-grid>
                  </n-list-item>
                </n-list>
              </n-gi>
            </n-grid>
          </n-tab-pane>
          <n-tab-pane name="上证指数" tab="上证指数">
            <k-line-chart code="sh000001" :chart-height="panelHeight" stockName="上证指数" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="深证成指" tab="深证成指">
            <k-line-chart code="sz399001" :chart-height="panelHeight" stockName="深证成指" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="创业板指" tab="创业板指">
            <k-line-chart code="sz399006" :chart-height="panelHeight" stockName="创业板指" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="恒生指数" tab="恒生指数">
            <k-line-chart code="hkHSI" :chart-height="panelHeight" stockName="恒生指数" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="纳斯达克" tab="纳斯达克">
            <k-line-chart code="us.IXIC" :chart-height="panelHeight" stockName="纳斯达克" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="道琼斯" tab="道琼斯">
            <k-line-chart code="us.DJI" :chart-height="panelHeight" stockName="道琼斯" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="标普500" tab="标普500">
            <k-line-chart code="us.INX" :chart-height="panelHeight" stockName="标普500" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
        </n-tabs>
      </n-tab-pane>
      <n-tab-pane name="重大指数" tab="重大指数">
        <n-tabs type="segment" animated>
          <n-tab-pane name="恒生科技指数" tab="恒生科技指数">
            <k-line-chart code="hkHSTECH" :chart-height="panelHeight" stockName="恒生科技指数" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="科创50" tab="科创50"  >
            <k-line-chart code="sh000688" :chart-height="panelHeight" stockName="科创50" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="科创芯片" tab="科创芯片"  >
            <k-line-chart code="sh000685" :chart-height="panelHeight" stockName="科创芯片" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="证券龙头" tab="证券龙头"  >
            <k-line-chart code="sz399437" :chart-height="panelHeight" stockName="证券龙头" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="高端装备" tab="高端装备"  >
            <k-line-chart code="sz399437" :chart-height="panelHeight" stockName="高端装备" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="中证银行" tab="中证银行">
            <k-line-chart code="sz399986" :chart-height="panelHeight" stockName="中证银行" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="上证医药" tab="上证医药">
            <k-line-chart code="sh000037" :chart-height="panelHeight" stockName="上证医药" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="沪深300" tab="沪深300">
            <k-line-chart code="sh000300" :chart-height="panelHeight" stockName="沪深300" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="上证50" tab="上证50">
            <k-line-chart code="sh000016" :chart-height="panelHeight" stockName="上证50" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="中证A500" tab="中证A500">
            <k-line-chart code="sh000510" :chart-height="panelHeight" stockName="中证A500" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="中证1000" tab="中证1000">
            <k-line-chart code="sh000852" :chart-height="panelHeight" stockName="中证1000" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="中证白酒" tab="中证白酒">
            <k-line-chart code="sz399997" :chart-height="panelHeight" stockName="中证白酒" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="富时中国三倍做多" tab="富时中国三倍做多">
            <k-line-chart code="usYINN.AM" :chart-height="panelHeight" stockName="富时中国三倍做多" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
          <n-tab-pane name="VIX恐慌指数" tab="VIX恐慌指数">
            <k-line-chart code="usUVXY.AM" :chart-height="panelHeight" stockName="VIX恐慌指数" :k-days="20"
                          :dark-theme="true"></k-line-chart>
          </n-tab-pane>
        </n-tabs>
      </n-tab-pane>
      <n-tab-pane name="行业排名" tab="行业排名">
        <n-tabs type="card" animated>
          <n-tab-pane name="行业涨幅排名" tab="行业涨幅排名">
            <n-table striped>
              <n-thead>
                <n-tr>
                  <n-th>行业名称</n-th>
                  <n-th @click="changeIndustryRankSort">行业涨幅
                    <n-icon v-if="sort==='0'" :component="CaretDown"/>
                    <n-icon v-if="sort==='1'" :component="CaretUp"/>
                  </n-th>
                  <n-th>行业5日涨幅</n-th>
                  <n-th>行业20日涨幅</n-th>
                  <n-th>领涨股</n-th>
                  <n-th>涨幅</n-th>
                  <n-th>最新价</n-th>
                </n-tr>
              </n-thead>
              <n-tbody>
                <n-tr v-for="item in industryRanks" :key="item.bd_code">
                  <n-td>
                    <n-tag :bordered=false type="info">{{ item.bd_name }}</n-tag>
                  </n-td>
                  <n-td>
                    <n-text :type="item.bd_zdf>0?'error':'success'">{{ item.bd_zdf }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.bd_zdf5>0?'error':'success'">{{ item.bd_zdf5 }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.bd_zdf20>0?'error':'success'">{{ item.bd_zdf20 }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.nzg_zdf>0?'error':'success'"> {{ item.nzg_name }}
                      <n-text type="info">{{ item.nzg_code }}</n-text>
                    </n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.nzg_zdf>0?'error':'success'"> {{ item.nzg_zdf }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.nzg_zdf>0?'error':'success'">{{ item.nzg_zxj }}</n-text>
                  </n-td>
                </n-tr>
              </n-tbody>
            </n-table>
            <n-table striped>
              <n-thead>
                <n-tr>
                  <n-th>行业名称</n-th>
                  <n-th @click="changeIndustryRankSort">行业涨幅
                    <n-icon v-if="sort==='0'" :component="CaretDown"/>
                    <n-icon v-if="sort==='1'" :component="CaretUp"/>
                  </n-th>
                  <n-th>行业5日涨幅</n-th>
                  <n-th>行业20日涨幅</n-th>
                  <n-th>领涨股</n-th>
                  <n-th>涨幅</n-th>
                  <n-th>最新价</n-th>
                </n-tr>
              </n-thead>
              <n-tbody>
                <n-tr v-for="item in industryRanks" :key="item.bd_code">
                  <n-td>
                    <n-tag :bordered=false type="info">{{ item.bd_name }}</n-tag>
                  </n-td>
                  <n-td>
                    <n-text :type="item.bd_zdf>0?'error':'success'">{{ item.bd_zdf }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.bd_zdf5>0?'error':'success'">{{ item.bd_zdf5 }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.bd_zdf20>0?'error':'success'">{{ item.bd_zdf20 }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.nzg_zdf>0?'error':'success'"> {{ item.nzg_name }}
                      <n-text type="info">{{ item.nzg_code }}</n-text>
                    </n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.nzg_zdf>0?'error':'success'"> {{ item.nzg_zdf }}%</n-text>
                  </n-td>
                  <n-td>
                    <n-text :type="item.nzg_zdf>0?'error':'success'">{{ item.nzg_zxj }}</n-text>
                  </n-td>
                </n-tr>
              </n-tbody>
            </n-table>
          </n-tab-pane>
          <n-tab-pane name="行业资金排名(净流入)" tab="行业资金排名">
            <industryMoneyRank :fenlei="'0'" :header-title="'行业资金排名(净流入)'" :sort="'netamount'"/>
          </n-tab-pane>
          <n-tab-pane name="证监会行业资金排名(净流入)" tab="证监会行业资金排名">
            <industryMoneyRank :fenlei="'2'" :header-title="'证监会行业资金排名(净流入)'" :sort="'netamount'"/>
          </n-tab-pane>
          <n-tab-pane name="概念板块资金排名(净流入)" tab="概念板块资金排名">
            <industryMoneyRank :fenlei="'1'" :header-title="'概念板块资金排名(净流入)'" :sort="'netamount'"/>
          </n-tab-pane>
        </n-tabs>
      </n-tab-pane>
      <n-tab-pane name="个股资金流向" tab="个股资金流向">
        <n-tabs type="card" animated>
          <n-tab-pane name="netamount" tab="净流入额排名">
            <RankTable :header-title="'净流入额排名'" :sort="'netamount'"/>
          </n-tab-pane>
          <n-tab-pane name="outamount" tab="流出资金排名">
            <RankTable :header-title="'流出资金排名'" :sort="'outamount'"/>
          </n-tab-pane>
          <n-tab-pane name="ratioamount" tab="净流入率排名">
            <RankTable :header-title="'净流入率排名'" :sort="'ratioamount'"/>
          </n-tab-pane>
          <n-tab-pane name="r0_net" tab="主力净流入额排名">
            <RankTable :header-title="'主力净流入额排名'" :sort="'r0_net'"/>
          </n-tab-pane>
          <n-tab-pane name="r0_out" tab="主力流出排名">
            <RankTable :header-title="'主力流出排名'" :sort="'r0_out'"/>
          </n-tab-pane>
          <n-tab-pane name="r0_ratio" tab="主力净流入率排名">
            <RankTable :header-title="'主力净流入率排名'" :sort="'r0_ratio'"/>
          </n-tab-pane>
          <n-tab-pane name="r3_net" tab="散户净流入额排名">
            <RankTable :header-title="'散户净流入额排名'" :sort="'r3_net'"/>
          </n-tab-pane>
          <n-tab-pane name="r3_out" tab="散户流出排名">
            <RankTable :header-title="'散户流出排名'" :sort="'r3_out'"/>
          </n-tab-pane>
          <n-tab-pane name="r3_ratio" tab="散户净流入率排名">
            <RankTable :header-title="'散户净流入率排名'" :sort="'r3_ratio'"/>
          </n-tab-pane>
        </n-tabs>
      </n-tab-pane>
      <n-tab-pane name="龙虎榜" tab="龙虎榜">
        <LongTigerRankList />
      </n-tab-pane>
      <n-tab-pane name="个股研报" tab="个股研报">
        <StockResearchReportList :stock-code="stockCode"/>
      </n-tab-pane>
      <n-tab-pane name="公司公告" tab="公司公告 ">
        <StockNoticeList :stock-code="stockCode" />
      </n-tab-pane>
      <n-tab-pane name="行业研究" tab="行业研究 ">
        <IndustryResearchReportList/>
      </n-tab-pane>
      <n-tab-pane name="当前热门" tab="当前热门">
        <n-tabs type="card" animated>
          <n-tab-pane name="全球" tab="全球">
            <HotStockList :market-type="'10'"/>
          </n-tab-pane>
          <n-tab-pane name="沪深" tab="沪深">
            <HotStockList :market-type="'12'"/>
          </n-tab-pane>
          <n-tab-pane name="港股" tab="港股">
            <HotStockList :market-type="'13'"/>
          </n-tab-pane>
          <n-tab-pane name="美股" tab="美股">
            <HotStockList :market-type="'11'"/>
          </n-tab-pane>
          <n-tab-pane name="热门话题" tab="热门话题">
            <n-grid :cols="1" :y-gap="10">
              <n-grid-item>
                <HotTopics/>
              </n-grid-item>
<!--              <n-grid-item>-->
<!--                <HotEvents/>-->
<!--              </n-grid-item>-->
            </n-grid>
          </n-tab-pane>
          <n-tab-pane name="重大事件时间轴" tab="重大事件时间轴">
            <InvestCalendarTimeLine />
          </n-tab-pane>
          <n-tab-pane name="财经日历" tab="财经日历">
            <ClsCalendarTimeLine />
          </n-tab-pane>
        </n-tabs>
      </n-tab-pane>
      <n-tab-pane name="指标选股" tab="指标选股">
        <select-stock />
      </n-tab-pane>
      <n-tab-pane name="名站优选" tab="名站优选">
        <Stockhotmap />
      </n-tab-pane>
    </n-tabs>
  </n-card>
  <n-modal transform-origin="center" v-model:show="summaryModal" preset="card"
           class="market-summary-modal"
           style="width: min(960px, calc(100vw - 32px));"
           :title="'AI市场资讯总结'">
    <div class="summary-modal-body">
      <n-spin size="small" :show="loading" class="summary-preview-spin">
        <div class="summary-preview-shell">
          <MdPreview class="summary-preview" :modelValue="aiSummary" :theme="theme"/>
        </div>
      </n-spin>
    </div>
    <template #footer>
      <n-flex justify="space-between" ref="tipsRef">
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
            placeholder="请输入时间，逗号分隔，例如 09:30,11:30,18:00"
            style="width: 420px;text-align:left;"
        />
        <n-button size="tiny" type="primary" :loading="summaryCronSaving" @click="saveSummaryCronConfig">
          保存定时
        </n-button>
      </n-flex>
      <n-flex justify="left" v-if="summaryStatusText" style="margin-bottom: 10px">
        <n-text depth="2">{{ summaryStatusText }}</n-text>
      </n-flex>
      <n-flex justify="left" style="margin-bottom: 10px">
        <n-switch v-model:value="enableTools" :round="false">
          <template #checked>
            启用AI函数工具调用
          </template>
          <template #unchecked>
            不启用AI函数工具调用
          </template>
        </n-switch>
        <n-switch v-model:value="thinkingMode" :round="false">
          <template #checked>
            启用思考模式
          </template>
          <template #unchecked>
            不启用思考模式
          </template>
        </n-switch>


        <n-gradient-text type="error" style="margin-left: 10px">*AI函数工具调用可以增强AI获取数据的能力,但会消耗更多tokens。</n-gradient-text>
        <n-tag :bordered="false" type="info">{{ summaryOutputHint }}</n-tag>
      </n-flex>
      <n-flex justify="space-between" style="margin-bottom: 10px">
        <n-select style="width: 32%" v-model:value="aiConfigId" label-field="name" value-field="ID"
                  :disabled="summaryRunning"
                  :options="aiConfigs" placeholder="请选择AI模型服务配置"/>
        <n-select style="width: 32%" v-model:value="sysPromptId" label-field="name" value-field="ID"
                  :disabled="summaryRunning"
                  :options="sysPromptOptions" placeholder="请选择系统提示词"/>
        <n-select style="width: 32%" v-model:value="draftQuestion" label-field="name" value-field="content"
                  :disabled="summaryRunning"
                  :options="userPromptOptions" placeholder="请选择用户提示词" @update:value="onQuestionTemplateChange"/>
      </n-flex>
      <n-flex justify="right">
        <n-input v-model:value="draftQuestion" style="text-align: left" clearable
                 :disabled="summaryRunning"
                 @update:value="onQuestionInput"
                 type="textarea"
                 :show-count="true"
                 placeholder="请输入您的问题:例如 总结和分析股票市场新闻中的投资机会，并推荐3个A股，并给出买入区间、止盈区间、止损位与失效条件；结果将固定输出为市场主线/候选方向/风险提示/推荐结论/交易计划说明/推荐股票池"
                 :autosize="{
              minRows: 2,
              maxRows: 5
            }"
        />
        <n-button size="tiny" type="warning" :loading="summaryRunning" :disabled="summaryRunning" @click="reAiSummary">再次总结</n-button>
        <n-button size="tiny" type="primary" :loading="summaryEmailSending" :disabled="summaryRunning || summaryEmailSending" @click="sendMarketSummaryEmailAction">发送邮件</n-button>
        <n-button size="tiny" type="success" @click="copyToClipboard">复制到剪切板</n-button>
        <n-button size="tiny" type="primary" @click="saveAsMarkdown">保存为Markdown文件</n-button>
        <n-button size="tiny" type="error" @click="share">分享到项目社区</n-button>
      </n-flex>
    </template>
  </n-modal>

  <div style="position: fixed;bottom: 18px;right:25px;z-index: 10;" v-if="summaryBTN">
    <n-input-group>
      <n-button type="primary" :loading="summaryRunning" @click="openSummaryModal">
        <n-icon :component="PulseOutline"/> &nbsp;{{ summaryRunning ? 'AI总结运行中' : 'AI总结' }}
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
