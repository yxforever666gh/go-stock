<script setup>
import {computed, h, onBeforeUnmount, onMounted, reactive, ref, watch} from 'vue'
import {
  GetAiRecommendStocksYieldList,
  GetAiRecommendYieldMinuteChart,
  GetAiRecommendYieldErrorLogs,
  StartAiRecommendMinuteDownload
} from "../services/app-api";
import {NBadge, NText, useMessage} from "naive-ui";
import { useDraggableDataTableColumns } from "../composables/useDraggableDataTableColumns";
import AiRecommendYieldMinuteReplayChart from "./AiRecommendYieldMinuteReplayChart.vue";
import { useSharedResearchDateRange } from "../composables/useSharedResearchDateRange";

const message = useMessage()
const { researchDateRangeModel, researchDateRangeKey, initSharedResearchDateRange } = useSharedResearchDateRange()
const rangeReadyRef = ref(false)
const strategyCohortRef = ref('all')
const strategyCohortOptions = [
  { label: 'All', value: 'all' },
  { label: 'V1.3.1', value: 'current' },
  { label: 'Phase3-v3', value: 'phase3-v3' },
  { label: 'Legacy', value: 'legacy' }
]
const strategyCohortLabelMap = {
  current: 'V1.3.1',
  'v1.3.2': 'V1.3.1',
  '1.3.2': 'V1.3.1',
  'phase3-v4': 'V1.3.1',
  '1.3.1': 'V1.3.1',
  v4: 'V1.3.1',
  'phase3-v3': 'Phase3-v3',
  legacy: 'Legacy',
  all: 'All'
}

const tableOverflowTooltip = {
  style: {
    maxWidth: 'min(560px, calc(100vw - 32px))'
  },
  contentStyle: {
    maxWidth: 'min(560px, calc(100vw - 32px))',
    whiteSpace: 'normal',
    wordBreak: 'break-word',
    overflowWrap: 'anywhere'
  }
}

const dataRef = ref([])
const loadingRef = ref(true)
const dataAsOfRef = ref("")
const recalcInProgressRef = ref(false)
const recalcProgressRef = ref(0)
const downloadInProgressRef = ref(false)
const downloadProgressRef = ref(0)
const downloadDoneRef = ref(0)
const downloadTotalRef = ref(0)
const minuteDownloadDoneRef = ref(0)
const minuteDownloadTotalRef = ref(0)
const minuteDownloadPendingRef = ref(0)
const minuteDownloadUncoverableRef = ref(0)
const diemengHealthStatusRef = ref("")
const diemengHealthSummaryRef = ref("尚未执行自检")
const diemengHealthCheckedAtRef = ref("")
const lastManualStartedAtRef = ref("")
const lastManualFinishedAtRef = ref("")
const lastManualScopeCountRef = ref(0)
const lastManualPrefetchMsRef = ref(0)
const lastManualRecalcMsRef = ref(0)
const lastManualTotalMsRef = ref(0)
const lastManualSqliteBusyCountRef = ref(0)
const lastManualProviderSummaryRef = ref("")
const lastManualAuditReadyRef = ref(false)
const strictPendingCountRef = ref(0)
const manualDownloadLoadingRef = ref(false)
const manualCooldownRemainSecRef = ref(0)
const errorLogModalVisibleRef = ref(false)
const errorLogLoadingRef = ref(false)
const errorLogDataRef = ref([])
const replayModalVisibleRef = ref(false)
const replayModalLoadingRef = ref(false)
const replayChartDataRef = ref(null)
const replayModalTitleRef = ref("")
let pollTimer = null
let cooldownTimer = null
let manualCooldownUntilMs = 0

const defaultColumns = [
  {
    title: '股票名称',
    key: 'stockName',
    minWidth: 120,
    render(row) {
      const repeat = Number(row?.recommendCount || 0)
      const recommendId = Number(row?.recommendId || 0)
      const nameNode = recommendId > 0
        ? h("span", {
          class: "yield-stock-link",
          title: "点击查看分钟回放",
          role: "button",
          tabindex: 0,
          style: "color:#2080f0;font-weight:500;",
          onClick: () => handleOpenReplay(row),
          onKeydown: (event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault()
              handleOpenReplay(row)
            }
          }
        }, row.stockName || "--")
        : h("span", {
          class: "yield-stock-name",
          style: "color:#2080f0;font-weight:500;"
        }, row.stockName || "--")
      if (Number.isNaN(repeat) || repeat <= 1) {
        return nameNode
      }
      return h(NBadge, {
        value: repeat,
        max: 99,
        type: "error",
        processing: false
      }, {
        default: () => nameNode
      })
    }
  },
  {
    title: '股票代码',
    key: 'stockCode',
    minWidth: 100,
  },
  {
    title: '信号时间',
    key: 'signalTime',
    minWidth: 170,
    render(row) {
      return row.signalTime || row.recommendTime || "--"
    }
  },
  {
    title: '买入区间',
    key: 'recommendBuyPrice',
    minWidth: 130,
    render(row) {
      return formatRecommendBuyDisplay(row.recommendBuyPrice)
    }
  },
  {
    title: '止盈区间',
    key: 'stopProfitAmount',
    minWidth: 120,
    render(row) {
      return formatMoney(row.stopProfitAmount)
    }
  },
  {
    title: '止损位',
    key: 'stopLossAmount',
    minWidth: 110,
    render(row) {
      return formatMoney(row.stopLossAmount)
    }
  },
  {
    title: '买入依据',
    key: 'buySignal',
    minWidth: 280,
    ellipsis: {
      tooltip: tableOverflowTooltip
    },
    render(row) {
      return buyBasisPreview(row)
    }
  },
  {
    title: '失效条件',
    key: 'invalidSignal',
    minWidth: 220,
    ellipsis: {
      tooltip: tableOverflowTooltip
    },
    render(row) {
      return row.invalidSignal || "--"
    }
  },
  {
    title: '晨间复核',
    key: 'latestOpeningReview',
    minWidth: 220,
    ellipsis: {
      tooltip: tableOverflowTooltip
    },
    render(row) {
      return openingReviewPreviewText(row?.latestOpeningReview)
    }
  },
  {
    title: '激活状态',
    key: 'activationStatus',
    minWidth: 110,
    render(row) {
      if (isStrictRowPending(row)) {
        return h(NText, {type: 'warning'}, {default: () => '待回算'})
      }
      const status = normalizeActivationStatus(row.activationStatus)
      const typeMap = {
        activated: 'success',
        pending: 'warning',
        skipped: 'default',
        expired: 'default',
        invalid: 'error',
        ineligible: 'default'
      }
      const labelMap = {
        activated: '已激活',
        pending: '待激活',
        skipped: '已跳过',
        expired: '过期未触发',
        invalid: '无法回算',
        ineligible: '未纳入回测'
      }
      return h(NText, {type: typeMap[status] || 'default'}, {default: () => labelMap[status] || '--'})
    }
  },
  {
    title: '激活时间',
    key: 'activationTime',
    minWidth: 170,
    render(row) {
      if (isStrictRowPending(row)) {
        return "--"
      }
      return row.activationTime || row.buyTime || "--"
    }
  },
  {
    title: '买入价/区间',
    key: 'buyAmount',
    minWidth: 150,
    render(row) {
      if (isStrictRowPending(row)) {
        return h(NText, {type: 'warning'}, {default: () => '--'})
      }
      const textType = resolveBuySellVisualType(row)
      if (normalizeActivationStatus(row.activationStatus) === 'activated') {
        return h(NText, {type: textType}, {default: () => formatMoney(row.buyAmount)})
      }
      return h(NText, {type: textType}, {default: () => formatRecommendBuyDisplay(row.recommendBuyPrice)})
    }
  },
  {
    title: '卖出金额',
    key: 'sellAmount',
    minWidth: 124,
    render(row) {
      if (isStrictRowPending(row)) {
        return h(NText, {type: 'warning'}, {default: () => '待回算'})
      }
      const amount = getSellAmountLines(row)
      return h("div", {style: "line-height: 1.35;"}, [
        h("div", `止盈: ${amount.profit}`),
        h("div", {style: "margin-top: 2px;"}, `止损: ${amount.loss}`)
      ])
    }
  },
  {
    title: '卖出时间',
    key: 'sellTime',
    minWidth: 280,
    render(row) {
      if (isStrictRowPending(row)) {
        return h(NText, {type: 'warning'}, {default: () => "待回算"})
      }
      const activationStatus = normalizeActivationStatus(row.activationStatus)
      const textType = resolveBuySellVisualType(row)
      if (activationStatus === 'skipped') {
        const reason = skippedDisplayReason(row)
        return h("div", {style: "line-height: 1.35;"}, [
          h(NText, {type: textType}, {default: () => "已跳过"}),
          h("div", {
            style: "font-size: 12px; color: #666; margin-top: 2px; white-space: normal;"
          }, reason || "未激活，已按规则跳过")
        ])
      }
      if (activationStatus === 'expired') {
        const reason = skippedDisplayReason(row)
        return h("div", {style: "line-height: 1.35;"}, [
          h(NText, {type: textType}, {default: () => "过期未触发"}),
          h("div", {
            style: "font-size: 12px; color: #666; margin-top: 2px; white-space: normal;"
          }, reason || "超过有效期仍未触发主买入区")
        ])
      }
      if (activationStatus === 'ineligible') {
        return h(NText, {type: textType}, {default: () => "未纳入回测"})
      }
      if (activationStatus === 'invalid') {
        return h(NText, {type: textType}, {default: () => "无法回算"})
      }
      if (activationStatus !== 'activated') {
        return h(NText, {type: textType}, {default: () => "待激活"})
      }
      const sellTime = String(row.sellTime || "").trim()
      if (sellTime === "待激活") {
        return h(NText, {type: textType}, {default: () => "待激活"})
      }
      if (!sellTime || sellTime === "持有") {
        return h(NText, {type: textType}, {default: () => "持有"})
      }
      return h(NText, {type: textType}, {default: () => sellTime})
    }
  },
  {
    title: '当前价格',
    key: 'currentPrice',
    minWidth: 100,
    render(row) {
      if (isStrictRowPending(row) && Number(row.currentPrice || 0) <= 0) {
        return "--"
      }
      return formatMoney(row.currentPrice)
    }
  },
  {
    title: '净收益率',
    key: 'yieldRate',
    minWidth: 110,
    render(row) {
      if (isStrictRowPending(row)) {
        return h(NText, {type: "warning"}, {default: () => "待回算"})
      }
      if (!row.yieldRateText || row.yieldRateText === "--") {
        return h(NText, {type: "default"}, {default: () => "--"})
      }
      if (Number(row.yieldRate) > 0) {
        return h(NText, {type: "error"}, {default: () => row.yieldRateText})
      }
      if (Number(row.yieldRate) < 0) {
        return h(NText, {type: "success"}, {default: () => row.yieldRateText})
      }
      return h(NText, {type: "default"}, {default: () => row.yieldRateText})
    }
  },
  {
    title: '基准收益率',
    key: 'benchmarkYieldRate',
    minWidth: 110,
    render(row) {
      if (isStrictRowPending(row)) {
        return h(NText, {type: "warning"}, {default: () => "待回算"})
      }
      if (!row.benchmarkYieldRateText || row.benchmarkYieldRateText === "--") {
        return h(NText, {type: "default"}, {default: () => "--"})
      }
      if (Number(row.benchmarkYieldRate) > 0) {
        return h(NText, {type: "error"}, {default: () => row.benchmarkYieldRateText})
      }
      if (Number(row.benchmarkYieldRate) < 0) {
        return h(NText, {type: "success"}, {default: () => row.benchmarkYieldRateText})
      }
      return h(NText, {type: "default"}, {default: () => row.benchmarkYieldRateText})
    }
  },
  {
    title: '超额收益率',
    key: 'excessYieldRate',
    minWidth: 110,
    render(row) {
      if (isStrictRowPending(row)) {
        return h(NText, {type: "warning"}, {default: () => "待回算"})
      }
      if (!row.excessYieldRateText || row.excessYieldRateText === "--") {
        return h(NText, {type: "default"}, {default: () => "--"})
      }
      if (Number(row.excessYieldRate) > 0) {
        return h(NText, {type: "error"}, {default: () => row.excessYieldRateText})
      }
      if (Number(row.excessYieldRate) < 0) {
        return h(NText, {type: "success"}, {default: () => row.excessYieldRateText})
      }
      return h(NText, {type: "default"}, {default: () => row.excessYieldRateText})
    }
  },
  {
    title: '数据状态',
    key: 'dataSync',
    minWidth: 360,
    render(row) {
      const status = dataSyncStatus(row)
      const reason = dataSyncReason(row)
      return h("div", {style: "line-height: 1.35;"}, [
        h(NText, {type: status.type}, {default: () => status.label}),
        h("div", {
          style: "font-size: 12px; color: #666; margin-top: 2px; white-space: normal;"
        }, reason)
      ])
    }
  },
  {
    title: '板块概念',
    key: 'bkName',
    minWidth: 220,
    ellipsis: {
      tooltip: tableOverflowTooltip
    }
  }
]
const { tableRef, columnsRef } = useDraggableDataTableColumns(defaultColumns, 'ai-recommend-yield-columns')

const errorLogColumnsRef = ref([
  {
    title: '时间',
    key: 'time',
    width: 170
  },
  {
    title: '来源',
    key: 'source',
    width: 90,
  },
  {
    title: '股票',
    key: 'stock',
    minWidth: 160,
    render(row) {
      if (row.stockCode) {
        return `${row.stockName || "--"} ${row.stockCode}`
      }
      return row.stockName || "--"
    }
  },
  {
    title: '状态',
    key: 'status',
    minWidth: 110,
    render(row) {
      const status = String(row.status || "--")
      const type = status === "正常" ? "default" : "warning"
      return h(NText, {type}, {default: () => status})
    }
  },
  {
    title: '可读说明',
    key: 'reason',
    minWidth: 280,
    ellipsis: {
      tooltip: tableOverflowTooltip
    }
  },
  {
    title: '原始信息',
    key: 'rawReason',
    minWidth: 360,
    ellipsis: {
      tooltip: tableOverflowTooltip
    }
  }
])
const tableScrollX = 2550
const errorLogTableScrollX = 1200

const paginationReactive = reactive({
  page: 1,
  pageCount: 1,
  pageSize: 100,
  itemCount: 0,
  keyword: "",
  prefix({itemCount}) {
    return `${itemCount} 条记录`
  }
})

const startDateModel = computed({
  get() {
    const date = normalizePickerDate(researchDateRangeModel.value?.[0])
    return date ? date.getTime() : null
  },
  set(value) {
    const nextDate = normalizePickerDate(value)
    if (!nextDate) {
      return
    }
    const currentEnd = normalizePickerDate(researchDateRangeModel.value?.[1]) || nextDate
    if (nextDate.getTime() <= currentEnd.getTime()) {
      researchDateRangeModel.value = [nextDate, currentEnd]
      return
    }
    researchDateRangeModel.value = [nextDate, nextDate]
  }
})

const endDateModel = computed({
  get() {
    const date = normalizePickerDate(researchDateRangeModel.value?.[1])
    return date ? date.getTime() : null
  },
  set(value) {
    const nextDate = normalizePickerDate(value)
    if (!nextDate) {
      return
    }
    const currentStart = normalizePickerDate(researchDateRangeModel.value?.[0]) || nextDate
    if (nextDate.getTime() >= currentStart.getTime()) {
      researchDateRangeModel.value = [currentStart, nextDate]
      return
    }
    researchDateRangeModel.value = [nextDate, nextDate]
  }
})

onMounted(async () => {
  await initSharedResearchDateRange()
  rangeReadyRef.value = true
  await fetchYieldList(1)
})

onBeforeUnmount(() => {
  stopAutoRefresh()
  stopCooldownTimer()
})

function query({
                 page,
                 pageSize = 100,
                 keyword = "",
                 startDate = "",
                 endDate = "",
                 strategyCohort = "current"
               }) {
  return new Promise((resolve) => {
    GetAiRecommendStocksYieldList({
      "page": page,
      "pageSize": pageSize,
      "modelName": keyword,
      "stockName": keyword,
      "stockCode": keyword,
      "bkName": keyword,
      "startDate": startDate,
      "endDate": endDate,
      "yieldMode": "strict",
      "strategyCohort": strategyCohort
    }).then((res) => {
      resolve({
        pageCount: res.totalPages,
        data: res.list,
        total: res.total,
        dataAsOf: res.dataAsOf || "",
        recalcInProgress: !!res.recalcInProgress,
        recalcProgress: Number(res.recalcProgress || 0),
        downloadInProgress: !!res.downloadInProgress,
        downloadProgress: Number(res.downloadProgress || 0),
        downloadDone: Number(res.downloadDone || 0),
        downloadTotal: Number(res.downloadTotal || 0),
        minuteDownloadDone: Number(res.minuteDownloadDone || 0),
        minuteDownloadTotal: Number(res.minuteDownloadTotal || 0),
        minuteDownloadPending: Number(res.minuteDownloadPending || 0),
        minuteDownloadUncoverable: Number(res.minuteDownloadUncoverable || 0),
        diemengHealthStatus: res.diemengHealthStatus || "",
        diemengHealthSummary: res.diemengHealthSummary || "",
        diemengHealthCheckedAt: res.diemengHealthCheckedAt || "",
        lastManualStartedAt: res.lastManualStartedAt || "",
        lastManualFinishedAt: res.lastManualFinishedAt || "",
        lastManualScopeCount: Number(res.lastManualScopeCount || 0),
        lastManualPrefetchMs: Number(res.lastManualPrefetchMs || 0),
        lastManualRecalcMs: Number(res.lastManualRecalcMs || 0),
        lastManualTotalMs: Number(res.lastManualTotalMs || 0),
        lastManualSqliteBusyCount: Number(res.lastManualSqliteBusyCount || 0),
        lastManualProviderSummary: res.lastManualProviderSummary || "",
        lastManualAuditReady: !!res.lastManualAuditReady,
        manualCooldownUntil: res.manualCooldownUntil || "",
        manualCooldownRemainSec: Number(res.manualCooldownRemainSec || 0)
      })
    })
  })
}

function normalizePickerDate(value) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return null
  }
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function currentRangeParams() {
  const range = researchDateRangeModel.value || []
  return {
    startDate: formatDate(range[0]),
    endDate: formatDate(range[1])
  }
}

function fetchYieldList(page, options = {}) {
  const silent = !!options.silent
  const { startDate, endDate } = currentRangeParams()
  return query({
    page,
    pageSize: paginationReactive.pageSize,
    keyword: paginationReactive.keyword,
    startDate,
    endDate,
    strategyCohort: strategyCohortRef.value
  }).then((data) => {
    dataRef.value = data.data
    strictPendingCountRef.value = countStrictPendingRows(data.data)
    paginationReactive.page = page
    paginationReactive.pageCount = data.pageCount
    paginationReactive.itemCount = data.total
    dataAsOfRef.value = data.dataAsOf
    recalcInProgressRef.value = data.recalcInProgress
    recalcProgressRef.value = Number(data.recalcProgress || 0)
    downloadInProgressRef.value = data.downloadInProgress
    downloadProgressRef.value = Number(data.downloadProgress || 0)
    downloadDoneRef.value = Number(data.downloadDone || 0)
    downloadTotalRef.value = Number(data.downloadTotal || 0)
    minuteDownloadDoneRef.value = Number(data.minuteDownloadDone || 0)
    minuteDownloadTotalRef.value = Number(data.minuteDownloadTotal || 0)
    minuteDownloadPendingRef.value = Number(data.minuteDownloadPending || 0)
    minuteDownloadUncoverableRef.value = Number(data.minuteDownloadUncoverable || 0)
    diemengHealthStatusRef.value = data.diemengHealthStatus || ""
    diemengHealthSummaryRef.value = data.diemengHealthSummary || "尚未执行自检"
    diemengHealthCheckedAtRef.value = data.diemengHealthCheckedAt || ""
    lastManualStartedAtRef.value = data.lastManualStartedAt || ""
    lastManualFinishedAtRef.value = data.lastManualFinishedAt || ""
    lastManualScopeCountRef.value = Number(data.lastManualScopeCount || 0)
    lastManualPrefetchMsRef.value = Number(data.lastManualPrefetchMs || 0)
    lastManualRecalcMsRef.value = Number(data.lastManualRecalcMs || 0)
    lastManualTotalMsRef.value = Number(data.lastManualTotalMs || 0)
    lastManualSqliteBusyCountRef.value = Number(data.lastManualSqliteBusyCount || 0)
    lastManualProviderSummaryRef.value = data.lastManualProviderSummary || ""
    lastManualAuditReadyRef.value = !!data.lastManualAuditReady
    applyManualCooldown(data.manualCooldownUntil, data.manualCooldownRemainSec)
    if (recalcInProgressRef.value) {
      ensureAutoRefresh()
    } else {
      stopAutoRefresh()
    }
    if (!silent) {
      loadingRef.value = false
    }
  }).catch((e) => {
    console.error("fetchYieldList failed", e)
    if (!silent) {
      loadingRef.value = false
    }
  })
}

watch(researchDateRangeKey, async (nextKey, prevKey) => {
  if (!rangeReadyRef.value || !prevKey || nextKey === prevKey) {
    return
  }
  loadingRef.value = true
  await fetchYieldList(1)
})

watch(strategyCohortRef, async (nextValue, prevValue) => {
  if (!rangeReadyRef.value || !prevValue || nextValue === prevValue) {
    return
  }
  loadingRef.value = true
  await fetchYieldList(1)
})

function strategyCohortLabel() {
  return formatStrategyCohortLabel(strategyCohortRef.value)
}

function formatStrategyCohortLabel(value) {
  const key = String(value || '').trim().toLowerCase()
  return strategyCohortLabelMap[key] || '--'
}

function handlePageChange(currentPage) {
  if (loadingRef.value) {
    return
  }
  loadingRef.value = true
  fetchYieldList(currentPage)
}

function handleSearch() {
  if (loadingRef.value) {
    return
  }
  loadingRef.value = true
  fetchYieldList(1)
}

async function handleManualDownload() {
  if (manualDownloadLoadingRef.value) {
    return
  }
  manualDownloadLoadingRef.value = true
  try {
    const result = await StartAiRecommendMinuteDownload()
    const msg = result?.message || "已触发任务"
    if (result?.accepted) {
      message.success(msg)
    } else {
      message.info(msg)
    }
    applyManualCooldown(result?.cooldownUntil || "", Number(result?.cooldownRemainSec || 0))
    if (result?.inProgress) {
      recalcInProgressRef.value = true
      ensureAutoRefresh()
      await fetchYieldList(paginationReactive.page, {silent: true})
    }
  } catch (e) {
    console.error("handleManualDownload failed", e)
    message.error("触发失败，请稍后重试")
  } finally {
    manualDownloadLoadingRef.value = false
  }
}

async function loadErrorLogs() {
  if (errorLogLoadingRef.value) {
    return
  }
  errorLogLoadingRef.value = true
  try {
    const result = await GetAiRecommendYieldErrorLogs(200)
    const rows = Array.isArray(result) ? result : []
    errorLogDataRef.value = rows.map((item, idx) => ({
      rowKey: `${item?.time || '--'}-${item?.stockCode || '--'}-${idx}`,
      time: item?.time || "--",
      source: item?.source || "--",
      stockCode: item?.stockCode || "",
      stockName: item?.stockName || "",
      status: item?.status || "--",
      reason: item?.reason || "暂无可读说明",
      rawReason: item?.rawReason || "--"
    }))
  } catch (e) {
    console.error("loadErrorLogs failed", e)
    message.error("读取报错日志失败，请稍后重试")
  } finally {
    errorLogLoadingRef.value = false
  }
}

async function handleOpenErrorLogs() {
  errorLogModalVisibleRef.value = true
  await loadErrorLogs()
}

async function handleOpenReplay(row) {
  const recommendId = Number(row?.recommendId || 0)
  if (!recommendId) {
    message.warning("该行缺少 recommendId，暂时无法打开分钟回放")
    return
  }
  replayModalVisibleRef.value = true
  replayModalTitleRef.value = `${row?.stockName || '--'} ${row?.stockCode || ''}`.trim()
  await loadReplayChart(recommendId)
}

async function loadReplayChart(recommendId) {
  replayModalLoadingRef.value = true
  try {
    const result = await GetAiRecommendYieldMinuteChart(Number(recommendId || 0))
    replayChartDataRef.value = result || null
    const stockName = String(result?.stockName || "").trim()
    const stockCode = String(result?.stockCode || "").trim()
    if (stockName || stockCode) {
      replayModalTitleRef.value = `${stockName || '--'} ${stockCode}`.trim()
    }
  } catch (e) {
    console.error("loadReplayChart failed", e)
    replayChartDataRef.value = {
      recommendId: Number(recommendId || 0),
      chartStatus: "missing",
      message: "读取分钟回放失败，请稍后重试",
      bars: [],
      markers: []
    }
    message.error("读取分钟回放失败，请稍后重试")
  } finally {
    replayModalLoadingRef.value = false
  }
}

async function handleRefreshReplay() {
  const recommendId = Number(replayChartDataRef.value?.recommendId || 0)
  if (!recommendId) {
    return
  }
  await loadReplayChart(recommendId)
}

function parseDateTime(dateStr) {
  const match = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})$/.exec(String(dateStr || ""))
  if (!match) {
    return null
  }
  return new Date(
      Number(match[1]),
      Number(match[2]) - 1,
      Number(match[3]),
      Number(match[4]),
      Number(match[5]),
      Number(match[6])
  )
}

function applyManualCooldown(cooldownUntil, remainSec) {
  const remain = Number(remainSec || 0)
  if (remain > 0) {
    manualCooldownUntilMs = Date.now() + remain * 1000
    refreshManualCooldownCountdown()
    ensureCooldownTimer()
    return
  }
  const target = parseDateTime(cooldownUntil)
  if (!target) {
    manualCooldownUntilMs = 0
    manualCooldownRemainSecRef.value = 0
    stopCooldownTimer()
    return
  }
  manualCooldownUntilMs = target.getTime()
  refreshManualCooldownCountdown()
  if (manualCooldownRemainSecRef.value > 0) {
    ensureCooldownTimer()
  } else {
    stopCooldownTimer()
  }
}

function refreshManualCooldownCountdown() {
  if (!manualCooldownUntilMs || manualCooldownUntilMs <= 0) {
    manualCooldownRemainSecRef.value = 0
    return
  }
  const remainMs = manualCooldownUntilMs - Date.now()
  if (remainMs <= 0) {
    manualCooldownUntilMs = 0
    manualCooldownRemainSecRef.value = 0
    return
  }
  manualCooldownRemainSecRef.value = Math.ceil(remainMs / 1000)
}

function ensureCooldownTimer() {
  if (cooldownTimer) {
    return
  }
  cooldownTimer = window.setInterval(() => {
    refreshManualCooldownCountdown()
    if (manualCooldownRemainSecRef.value <= 0) {
      stopCooldownTimer()
    }
  }, 1000)
}

function stopCooldownTimer() {
  if (!cooldownTimer) {
    return
  }
  clearInterval(cooldownTimer)
  cooldownTimer = null
}

function formatCooldownMMSS(seconds) {
  const total = Math.max(0, Number(seconds || 0))
  const mins = Math.floor(total / 60)
  const secs = total % 60
  return `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`
}

function manualDownloadButtonText() {
  if (downloadInProgressRef.value) {
    const total = Number(downloadTotalRef.value || 0)
    const done = Number(downloadDoneRef.value || 0)
    const p = Math.max(0, Number(downloadProgressRef.value || 0))
    if (total > 0) {
      return `手动下载分钟线（下载中 ${p}% ${done}/${total}）`
    }
    return "手动下载分钟线（下载中）"
  }
  if (recalcInProgressRef.value) {
    const p = Math.max(0, Number(recalcProgressRef.value || 0))
    return `手动下载分钟线（回算中 ${p}%）`
  }
  if (manualCooldownRemainSecRef.value > 0) {
    return `手动下载分钟线 (${formatCooldownMMSS(manualCooldownRemainSecRef.value)})`
  }
  return "手动下载分钟线"
}

function isManualDownloadBusy() {
  return manualDownloadLoadingRef.value || recalcInProgressRef.value
}

function ensureAutoRefresh() {
  if (!recalcInProgressRef.value) {
    stopAutoRefresh()
    return
  }
  if (pollTimer) {
    return
  }
  pollTimer = window.setInterval(() => {
    if (loadingRef.value) {
      return
    }
    fetchYieldList(paginationReactive.page, {silent: true})
  }, 3000)
}

function stopAutoRefresh() {
  if (!pollTimer) {
    return
  }
  clearInterval(pollTimer)
  pollTimer = null
}

function minuteCoverageText() {
  const total = Number(minuteDownloadTotalRef.value || 0)
  const done = Number(minuteDownloadDoneRef.value || 0)
  const uncoverable = Number(minuteDownloadUncoverableRef.value || 0)
  const pending = Number(minuteDownloadPendingRef.value || 0)
  if (!total || total <= 0) {
    return "--"
  }
  return `${done}/${total}（不可覆盖:${uncoverable}，待覆盖:${pending}）`
}

function minuteCoveragePercentText() {
  const total = Number(minuteDownloadTotalRef.value || 0)
  const done = Number(minuteDownloadDoneRef.value || 0)
  if (!total || total <= 0) {
    return "--"
  }
  const pct = Math.round((done / total) * 100)
  return `${pct}%`
}

function taskProgressText() {
  if (downloadInProgressRef.value) {
    const total = Number(downloadTotalRef.value || 0)
    const done = Number(downloadDoneRef.value || 0)
    const p = Math.max(0, Number(downloadProgressRef.value || 0))
    if (total > 0) {
      return `下载中 ${p}%（${done}/${total}）`
    }
    return "下载中"
  }
  if (recalcInProgressRef.value) {
    const p = Number(recalcProgressRef.value || 0)
    return `回算中 ${p}%`
  }
  return "已结束"
}

function taskStatusHintText() {
  const pending = Number(minuteDownloadPendingRef.value || 0)
  const uncoverable = Number(minuteDownloadUncoverableRef.value || 0)
  if (downloadInProgressRef.value) {
    return "后台正在下载分钟线"
  }
  if (recalcInProgressRef.value) {
    return "后台正在回算收益率（覆盖进度不等于任务进度）"
  }
  if (pending > 0 || uncoverable > 0) {
    return "后台任务已结束，但仍有待覆盖/不可覆盖记录"
  }
  return "后台任务已结束，覆盖已完成"
}

function formatDurationMs(ms) {
  const total = Math.max(0, Number(ms || 0))
  if (!total) {
    return "--"
  }
  if (total < 1000) {
    return `${total}ms`
  }
  const seconds = Math.round(total / 100) / 10
  if (seconds < 60) {
    return `${seconds}s`
  }
  const mins = Math.floor(seconds / 60)
  const remain = Math.round((seconds - mins * 60) * 10) / 10
  if (remain <= 0) {
    return `${mins}m`
  }
  return `${mins}m${remain}s`
}

function lastManualSummaryText() {
  const hasStarted = !!String(lastManualStartedAtRef.value || '').trim()
  const hasFinished = !!String(lastManualFinishedAtRef.value || '').trim()
  const hasAnyTiming = Number(lastManualPrefetchMsRef.value || 0) > 0
    || Number(lastManualRecalcMsRef.value || 0) > 0
    || Number(lastManualTotalMsRef.value || 0) > 0
  if (!hasStarted && !hasFinished && !hasAnyTiming) {
    return "暂无手动任务记录"
  }
  const parts = []
  if (hasStarted) {
    parts.push(`开始 ${lastManualStartedAtRef.value}`)
  }
  if (hasFinished) {
    parts.push(`结束 ${lastManualFinishedAtRef.value}`)
  }
  if (lastManualScopeCountRef.value > 0) {
    parts.push(`scope ${lastManualScopeCountRef.value}`)
  }
  if (!lastManualAuditReadyRef.value) {
    if (recalcInProgressRef.value) {
      parts.push("执行中，详细审计待任务完成后写入")
      return parts.join("；")
    }
    parts.push("历史版本未记录详细耗时")
    return parts.join("；")
  }
  parts.push(`预取 ${formatDurationMs(lastManualPrefetchMsRef.value)}`)
  parts.push(`回算 ${formatDurationMs(lastManualRecalcMsRef.value)}`)
  parts.push(`总耗时 ${formatDurationMs(lastManualTotalMsRef.value)}`)
  parts.push(`busy ${Number(lastManualSqliteBusyCountRef.value || 0)}`)
  if (lastManualProviderSummaryRef.value) {
    parts.push(`源 ${lastManualProviderSummaryRef.value}`)
  }
  return parts.join("；")
}

function diemengHealthTextType() {
  const status = String(diemengHealthStatusRef.value || '').trim()
  if (status === 'ok') {
    return 'success'
  }
  if (status === 'degraded') {
    return 'warning'
  }
  if (status === 'error') {
    return 'error'
  }
  return 'default'
}

function formatDate(dateString) {
  const date = new Date(dateString)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function formatMoney(value) {
  const n = Number(value)
  if (Number.isNaN(n) || n <= 0) {
    return "--"
  }
  return n.toFixed(2)
}

function formatRecommendBuyDisplay(value) {
  const text = String(value || "").trim()
  if (!text) {
    return "--"
  }
  const matches = text.match(/\d+(?:\.\d+)?/g) || []
  if (matches.length >= 2) {
    const first = Number(matches[0])
    const second = Number(matches[1])
    if (Number.isFinite(first) && Number.isFinite(second)) {
      const min = Math.min(first, second).toFixed(2)
      const max = Math.max(first, second).toFixed(2)
      return min === max ? min : `${min}-${max}`
    }
  }
  if (matches.length === 1) {
    const single = Number(matches[0])
    if (Number.isFinite(single)) {
      return single.toFixed(2)
    }
  }
  return text
}

function isStrictRowPending(row) {
  return row?.strictReady === false
}

function strictPendingReason(row) {
  const reason = String(row?.strictPendingReason || '').trim()
  if (reason) {
    return reason
  }
  return '该股票存在待下载或待回算的严格模式任务'
}

function countStrictPendingRows(rows) {
  if (!Array.isArray(rows)) {
    return 0
  }
  return rows.filter((row) => isStrictRowPending(row)).length
}

function signalPreview(main, detail) {
  const text = [String(main || "").trim(), String(detail || "").trim()].filter(Boolean).join("；")
  return text || "--"
}

function buyBasisPreview(row) {
  return signalPreview(row.buySignal, row.buySignalDetail)
}

function normalizeSellAmountPart(value) {
  const text = String(value || "").trim()
  if (!text || text === "null" || text === "undefined") {
    return "--"
  }
  return text
}

function getSellAmountLines(row) {
  if (normalizeActivationStatus(row?.activationStatus) !== 'activated') {
    return {
      profit: "--",
      loss: "--"
    }
  }
  const sellAmountText = String(row?.sellAmountText || "").trim()
  if (sellAmountText) {
    const parts = sellAmountText.split("/")
    if (parts.length >= 2) {
      return {
        profit: normalizeSellAmountPart(parts[0]),
        loss: normalizeSellAmountPart(parts[1])
      }
    }
    if (parts.length === 1) {
      return {
        profit: normalizeSellAmountPart(parts[0]),
        loss: "--"
      }
    }
  }
  return {
    profit: formatMoney(row?.stopProfitAmount),
    loss: formatMoney(row?.stopLossAmount)
  }
}

function normalizeActivationStatus(status) {
  const text = String(status || '').trim().toLowerCase()
  if (!text) {
    return 'pending'
  }
  if (text === 'activated') {
    return 'activated'
  }
  if (text === 'skipped') {
    return 'skipped'
  }
  if (text === 'expired') {
    return 'expired'
  }
  if (text === 'ineligible') {
    return 'ineligible'
  }
  if (text === 'invalid') {
    return 'invalid'
  }
  return 'pending'
}

function resolveBuySellVisualType(row) {
  if (isStrictRowPending(row)) {
    return 'warning'
  }
  const activationStatus = normalizeActivationStatus(row?.activationStatus)
  if (activationStatus === 'skipped' || activationStatus === 'expired') {
    return 'default'
  }
  if (activationStatus === 'ineligible') {
    return 'default'
  }
  if (activationStatus === 'invalid') {
    return 'error'
  }
  if (activationStatus !== 'activated') {
    return 'warning'
  }

  const sellTime = String(row?.sellTime || '').trim()
  if (sellTime === '待激活') {
    return 'warning'
  }
  if (!sellTime || sellTime === '持有') {
    return 'info'
  }

  const y = Number(row?.yieldRate)
  if (!Number.isNaN(y)) {
    if (y > 0) {
      return 'error'
    }
    if (y < 0) {
      return 'success'
    }
  }
  return 'default'
}

function isDataFullySynced(row) {
  if (isStrictRowPending(row)) {
    return false
  }
  const status = String(row?.dataStatus || "").trim()
  return status === "" || status === "正常" || status === "已跳过" || status === "已过期" || status === "已失效" || status === "未结构化"
}

function dataSyncStatus(row) {
  if (isStrictRowPending(row)) {
    return {
      label: '待回算',
      type: 'warning'
    }
  }
  if (isDataFullySynced(row)) {
    return {
      label: '已完成',
      type: 'success'
    }
  }
  return {
    label: '未完成',
    type: 'warning'
  }
}

function dataSyncReason(row) {
  if (isStrictRowPending(row)) {
    return strictPendingReason(row)
  }
  const status = String(row?.dataStatus || "").trim()
  const reason = String(row?.dataStatusReason || "").trim()
  if (normalizeActivationStatus(row?.activationStatus) === 'ineligible') {
    return reason || row?.backtestEligibilityReason || "该推荐未形成可机械执行交易计划，未纳入回测统计"
  }
  if (status === "已跳过") {
    return "未激活结果已同步"
  }
  if (status === "已过期") {
    return "过期未触发结果已同步"
  }
  if (status === "已失效") {
    return "失效结果已同步"
  }
  if (isDataFullySynced(row)) {
    return "分钟线交易时段连续性已校验，数据已更新"
  }
  if (reason) {
    return reason
  }
  if (status === "计算中") {
    return "后台任务仍在计算中，请稍后刷新"
  }
  if (status === "待覆盖") {
    return "分钟线目标时间段尚未覆盖"
  }
  if (status === "不可覆盖" || status === "无法判定") {
    return "当前分钟线无法覆盖目标时间段"
  }
  return "数据尚未更新完成"
}

function skippedDisplayReason(row) {
  const reason = String(row?.dataStatusReason || '').trim()
  if (reason) {
    return reason
  }
  return "未激活，已按规则跳过"
}

function openingReviewActionText(review) {
  const action = String(review?.action || '').trim()
  const map = {
    continue_plan: '继续按原计划执行',
    observe_only: '继续观察，不提前激活',
    cancel_plan: '取消隔夜计划',
    hold: '继续持有',
    reduce_risk: '优先风控/止盈'
  }
  return map[action] || action || '--'
}

function openingReviewPreviewText(review) {
  if (!review) {
    return '--'
  }
  const action = openingReviewActionText(review)
  const reason = String(review?.reason || review?.rawSummary || '').trim()
  if (!reason) {
    return action
  }
  return `${action}；${reason}`
}

function replayChartStatusText() {
  const status = String(replayChartDataRef.value?.chartStatus || '').trim()
  if (status === 'ready') {
    return '分钟线完整'
  }
  if (status === 'partial') {
    return '分钟线部分缺失'
  }
  if (status === 'unsupported') {
    return '当前记录无法回放'
  }
  if (status === 'missing') {
    return '缺少分钟线'
  }
  return '--'
}

function replayAlertType() {
  const status = String(replayChartDataRef.value?.chartStatus || '').trim()
  if (status === 'ready') {
    return 'info'
  }
  if (status === 'partial') {
    return 'warning'
  }
  if (status === 'unsupported' || status === 'missing') {
    return 'warning'
  }
  return 'default'
}

function replayRangeText() {
  const rangeLabel = String(replayChartDataRef.value?.rangeLabel || '').trim()
  if (rangeLabel) {
    return rangeLabel
  }
  return '--'
}

function replayMarkerSummaryText() {
  const markers = Array.isArray(replayChartDataRef.value?.markers) ? replayChartDataRef.value.markers : []
  if (markers.length === 0) {
    return '--'
  }
  return markers.map((item) => {
    const label = String(item?.label || '--').trim()
    const status = String(item?.status || '').trim()
    let suffix = ''
    if (status === 'approximated' && label !== '信号') {
      suffix = '（顺延）'
    }
    return `${label}${suffix}`
  }).join('、')
}

</script>

<template>
  <n-input-group>
    <n-date-picker v-model:value="startDateModel" type="date" style="width: 22%" />
    <n-input value="至" readonly style="width: 8%; text-align: center;" />
    <n-date-picker v-model:value="endDateModel" type="date" style="width: 22%" />
    <n-select
        v-model:value="strategyCohortRef"
        :options="strategyCohortOptions"
        style="width: 20%"
    />
    <n-input clearable placeholder="输入关键词搜索" v-model:value="paginationReactive.keyword"/>
    <n-button type="primary" ghost @click="handleSearch" @input="handleSearch">
      搜索
    </n-button>
    <n-button
        type="warning"
        ghost
        :loading="isManualDownloadBusy()"
        :disabled="isManualDownloadBusy() || manualCooldownRemainSecRef > 0"
        @click="handleManualDownload"
    >
      {{ manualDownloadButtonText() }}
    </n-button>
    <n-button
        type="info"
        ghost
        :loading="errorLogLoadingRef"
        @click="handleOpenErrorLogs"
    >
      报错日志
    </n-button>
  </n-input-group>
  <div style="margin-top: 8px;">
    <n-text depth="3">当前分层：{{ strategyCohortLabel() }}</n-text>
    <n-text depth="3" style="margin-left: 12px;">默认查看全部阶段；可切换 V1.3.1 或历史阶段对比不同策略阶段。</n-text>
  </div>
  <div style="margin-top: 6px;">
    <n-text depth="3">当前口径：严格回算</n-text>
    <n-text depth="3" style="margin-left: 12px;">待回算：{{ strictPendingCountRef }}</n-text>
    <n-text depth="3" style="margin-left: 12px;">strict 只读已落库严格快照；待回算股票需补齐交易时段连续分钟线并等待后台刷新。</n-text>
  </div>
  <div style="margin-top: 6px;">
    <n-text depth="3">最近一次手动任务：</n-text>
    <n-text depth="3">{{ lastManualSummaryText() }}</n-text>
  </div>
  <div style="margin-top: 6px;">
    <n-text depth="3">数据时间：{{ dataAsOfRef || "--" }}</n-text>
    <n-text depth="3" style="margin-left: 12px;">分钟线连续覆盖：{{ minuteCoverageText() }}</n-text>
    <n-text depth="3" style="margin-left: 12px;">连续覆盖进度：{{ minuteCoveragePercentText() }}</n-text>
    <n-text depth="3" style="margin-left: 12px;">任务进度：{{ taskProgressText() }}</n-text>
    <n-text depth="3" style="margin-left: 12px;">{{ taskStatusHintText() }}</n-text>
  </div>
  <n-data-table
      ref="tableRef"
      remote
      size="small"
      :columns="columnsRef"
      :data="dataRef"
      :loading="loadingRef"
      :pagination="paginationReactive"
      :scroll-x="tableScrollX"
      :row-key="(rowData) => rowData.rowKey || rowData.stockCode"
      @update:page="handlePageChange"
      flex-height
      style="height: calc(100vh - 210px);margin-top: 10px"
  />

  <n-modal
      transform-origin="center"
      v-model:show="replayModalVisibleRef"
      preset="card"
      style="width: 1180px;"
      :title="replayModalTitleRef || '分钟收益回放'"
  >
    <div style="margin-bottom: 10px; text-align: right;">
      <n-button size="small" ghost type="primary" :loading="replayModalLoadingRef" @click="handleRefreshReplay">
        刷新回放
      </n-button>
      <n-button
          size="small"
          ghost
          type="warning"
          style="margin-left: 8px;"
          :loading="isManualDownloadBusy()"
          :disabled="isManualDownloadBusy() || manualCooldownRemainSecRef > 0"
          @click="handleManualDownload"
      >
        {{ manualDownloadButtonText() }}
      </n-button>
    </div>
    <n-spin :show="replayModalLoadingRef">
      <div v-if="replayChartDataRef">
        <div style="margin-bottom: 10px;">
          <n-text depth="3">区间：{{ replayRangeText() }}</n-text>
          <n-text depth="3" style="margin-left: 12px;">状态：{{ replayChartStatusText() }}</n-text>
          <n-text depth="3" style="margin-left: 12px;">标记：{{ replayMarkerSummaryText() }}</n-text>
        </div>
        <div style="margin-bottom: 12px;">
          <n-text depth="3">信号时间：{{ replayChartDataRef.signalTime || "--" }}</n-text>
          <n-text depth="3" style="margin-left: 12px;">买入时间：{{ replayChartDataRef.buyTime || "--" }}</n-text>
          <n-text depth="3" style="margin-left: 12px;">卖出/当前：{{ replayChartDataRef.sellTime || replayChartDataRef.currentPriceTime || "--" }}</n-text>
        </div>
        <n-alert
            v-if="replayChartDataRef.latestOpeningReview"
            type="warning"
            :show-icon="false"
            style="margin-bottom: 12px; text-align: left;"
        >
          <div>09:40 开盘复核：{{ openingReviewActionText(replayChartDataRef.latestOpeningReview) }}</div>
          <div style="margin-top: 4px;">
            开盘价 {{ replayChartDataRef.latestOpeningReview.openingPrice || '--' }}，
            竞价价 {{ replayChartDataRef.latestOpeningReview.auctionPrice || '--' }}，
            09:40 最新价 {{ replayChartDataRef.latestOpeningReview.minutePrice || '--' }}
          </div>
          <div style="margin-top: 4px;">原因：{{ replayChartDataRef.latestOpeningReview.reason || replayChartDataRef.latestOpeningReview.rawSummary || '--' }}</div>
        </n-alert>
        <n-alert
            v-if="replayChartDataRef.message"
            :type="replayAlertType()"
            :show-icon="false"
            style="margin-bottom: 12px; text-align: left;"
        >
          {{ replayChartDataRef.message }}
        </n-alert>
        <ai-recommend-yield-minute-replay-chart :chart-data="replayChartDataRef"/>
      </div>
      <n-empty v-else description="暂无回放数据"></n-empty>
    </n-spin>
  </n-modal>

  <n-modal
      transform-origin="center"
      v-model:show="errorLogModalVisibleRef"
      preset="card"
      style="width: 1100px;"
      title="股票收益率报错日志"
  >
    <div style="margin-bottom: 8px; text-align: right;">
      <n-button size="small" ghost type="primary" :loading="errorLogLoadingRef" @click="loadErrorLogs">
        刷新日志
      </n-button>
    </div>
    <n-data-table
        size="small"
        :columns="errorLogColumnsRef"
        :data="errorLogDataRef"
        :row-key="(rowData) => rowData.rowKey"
        :pagination="{ pageSize: 10 }"
        :scroll-x="errorLogTableScrollX"
        max-height="560"
    />
  </n-modal>
</template>

<style scoped>
.yield-stock-link {
  color: #2080f0;
  cursor: pointer;
  font-weight: 500;
  transition: color 0.18s ease, text-decoration-color 0.18s ease, opacity 0.18s ease;
}

.yield-stock-name {
  color: #2080f0;
  font-weight: 500;
}

.yield-stock-link:hover {
  color: #4098fc;
  text-decoration: underline;
  text-decoration-thickness: 1.5px;
  text-underline-offset: 3px;
}

.yield-stock-link:focus-visible {
  outline: none;
  color: #4098fc;
  text-decoration: underline;
  text-decoration-thickness: 1.5px;
  text-underline-offset: 3px;
}

:deep(.draggable-column-title) {
  display: inline-flex;
  align-items: center;
  cursor: move;
  user-select: none;
}

:deep(.n-data-table-th.column-drag-over) {
  background-color: #edf6ff;
  box-shadow: inset 0 0 0 1px #3b82f6;
}

:deep(.n-data-table-th.column-dragging) {
  opacity: 0.55;
}
</style>
