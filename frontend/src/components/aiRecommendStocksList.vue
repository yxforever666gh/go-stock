<script setup>
import {onBeforeMount, onMounted, reactive, ref, h, watch} from 'vue'
import {
  GetAiRecommendStocksList,
  GetConfig,
  DeleteAiRecommendStocks,
} from "../services/app-api";
import {NAlert, NButton, NDatePicker, NDivider, NGradientText, NInput, NInputGroup, NTag, NText, useNotification} from "naive-ui";
import KLineChart from "./KLineChart.vue";
import { useDraggableDataTableColumns } from "../composables/useDraggableDataTableColumns";
import { useSharedResearchDateRange } from "../composables/useSharedResearchDateRange";

const notify = useNotification()
const { researchDateRangeModel, researchDateRangeKey, initSharedResearchDateRange } = useSharedResearchDateRange()
const rangeReadyRef = ref(false)
const editorDataRef = reactive({
  darkTheme: false,
})
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
const tableScrollX = 2500
const strategyCohortRef = ref('all')
const strategyCohortOptions = [
  { label: 'All', value: 'all' },
  { label: 'V1.5.0', value: '1.5.0' },
  { label: 'V1.4.2', value: '1.4.2' },
  { label: 'V1.4.1', value: '1.4.1' },
  { label: 'V1.4.0', value: '1.4.0' },
  { label: 'V1.3.6', value: '1.3.6' },
  { label: 'V1.3.2', value: 'v1.3.2' },
  { label: 'V1.3.1', value: 'phase3-v4' }
]

onBeforeMount(() => {
  GetConfig().then(result => {
    if (result.darkTheme) {
      editorDataRef.darkTheme = true
    }
  })
})

onMounted(async () => {
  await initSharedResearchDateRange()
  rangeReadyRef.value = true
  await refreshList(1)
})

const defaultColumns = [
  {
    title: '推荐模型',
    key: 'modelName',
    minWidth: 120,
    render(row) {
      const label = formatProviderModelLabel(row.providerName, row.modelName)
      return h(NText, {type: "info"}, {default: () => label})
    }
  },
  {
    title: '推荐时间',
    key: 'dataTime',
    minWidth: 160,
    render(row) {
      return formatDateTime(row.CreatedAt)
    }
  },
  {
    title: '所属方向',
    key: 'bkName',
    minWidth: 120,
    ellipsis: { tooltip: tableOverflowTooltip }
  },
  {
    title: '股票名称',
    key: 'stockName',
    minWidth: 110,
    render(row) {
      return h(NText, {type: "info"}, {default: () => row.stockName || '--'})
    }
  },
  {
    title: '股票代码',
    key: 'stockCode',
    width: 120,
  },
  {
    title: '当前/锚点',
    key: 'stockCurrentPrice',
    minWidth: 130,
    render(row) {
      const current = normalizeNumber(row.stockCurrentPrice)
      const observe = row.observePrice || row.stockPrice || '--'
      if (!current) {
        return `${row.stockCurrentPrice || '--'} / ${observe}`
      }
      return `${row.stockCurrentPrice} / ${observe}`
    }
  },
  {
    title: '买入区间',
    key: 'recommendBuyPrice',
    minWidth: 130,
    render(row) {
      return row.recommendBuyPrice || '--'
    }
  },
  {
    title: '止盈区间',
    key: 'recommendStopProfitPrice',
    minWidth: 130,
    render(row) {
      return row.recommendStopProfitPrice || '--'
    }
  },
  {
    title: '止损位',
    key: 'recommendStopLossPrice',
    width: 110,
    render(row) {
      return row.recommendStopLossPrice || '--'
    }
  },
  {
    title: '买入依据',
    key: 'buySignal',
    minWidth: 260,
    ellipsis: { tooltip: tableOverflowTooltip },
    render(row) {
      return buyBasisPreview(row)
    }
  },
  {
    title: '失效条件',
    key: 'invalidSignal',
    minWidth: 220,
    ellipsis: { tooltip: tableOverflowTooltip },
    render(row) {
      return invalidConditionPreview(row)
    }
  },
  {
    title: '预期周期',
    key: 'expectedCycle',
    width: 110,
    render(row) {
      return row.expectedCycle || '--'
    }
  },
  {
    title: '核心催化',
    key: 'coreCatalyst',
    minWidth: 180,
    ellipsis: { tooltip: tableOverflowTooltip },
    render(row) {
      return row.coreCatalyst || extractLineValue(row.recommendReason, ['核心催化：', '核心逻辑：']) || '--'
    }
  },
  {
    title: '风险提示',
    key: 'riskRemarks',
    minWidth: 180,
    ellipsis: { tooltip: tableOverflowTooltip }
  },
  {
    title: '操作',
    width: 130,
    render(row) {
      return [
        h(NTag, {
          strong: true,
          tertiary: true,
          size: 'small',
          type: 'warning',
          style: 'margin-right: 8px;',
          onClick: () => showDetail(row)
        }, {default: () => '查看'}),
        h(NTag, {
          strong: true,
          tertiary: true,
          size: 'small',
          type: 'error',
          onClick: () => deleteAiRecommendStocks(row.ID)
        }, {default: () => '删除'})
      ]
    }
  },
]
const { tableRef, columnsRef } = useDraggableDataTableColumns(defaultColumns, 'ai-recommend-record-columns')

const paginationReactive = reactive({
  page: 1,
  pageCount: 1,
  pageSize: 12,
  itemCount: 0,
  keyword: "",
  prefix({itemCount}) {
    return `${itemCount} 条记录`
  }
})

const modalDataRef = reactive({
  visible: false,
  title: "",
  stockCode: "",
  stockName: "",
  coreCatalyst: "",
  keyEvidence: "",
  evidenceSources: [],
  invalidCondition: "",
  observePrice: "",
  buySignal: "",
  buySignalDetail: "",
  sellSignal: "",
  sellSignalDetail: "",
  invalidSignal: "",
  recommendBuyPrice: "",
  stopProfitPrice: "",
  stopLossPrice: "",
  expectedCycle: "",
  riskRemarks: "",
  recommendReason: "",
  remarks: "",
  eventStrength: 0,
  capitalConfirmation: 0,
  fundamentalFit: 0,
  technicalFit: 0,
  latestOpeningReview: null,
})

function query({
  page,
  pageSize = 10,
  keyword = "",
  startDate = "",
  endDate = "",
  strategyCohort = "all"
}) {
  return new Promise((resolve) => {
    GetAiRecommendStocksList({
      page,
      pageSize,
      modelName: keyword,
      stockName: keyword,
      stockCode: keyword,
      bkName: keyword,
      startDate,
      endDate,
      strategyCohort
    }).then((res) => {
      resolve({
        pageCount: res.totalPages,
        data: res.list,
        total: res.total
      })
    })
  })
}

function currentRangeParams() {
  const range = researchDateRangeModel.value || []
  return {
    startDate: formatDate(range[0]),
    endDate: formatDate(range[1])
  }
}

async function refreshList(page) {
  loadingRef.value = true
  const { startDate, endDate } = currentRangeParams()
  const data = await query({
    page,
    pageSize: paginationReactive.pageSize,
    keyword: paginationReactive.keyword,
    startDate,
    endDate,
    strategyCohort: strategyCohortRef.value
  })
  dataRef.value = data.data
  paginationReactive.page = page
  paginationReactive.pageCount = data.pageCount
  paginationReactive.itemCount = data.total
  loadingRef.value = false
}

watch(researchDateRangeKey, async (nextKey, prevKey) => {
  if (!rangeReadyRef.value || !prevKey || nextKey === prevKey) {
    return
  }
  await refreshList(1)
})

watch(strategyCohortRef, async (nextValue, prevValue) => {
  if (!rangeReadyRef.value || !prevValue || nextValue === prevValue) {
    return
  }
  await refreshList(1)
})

function handlePageChange(currentPage) {
  if (loadingRef.value) return
  refreshList(currentPage)
}

function handleSearch() {
  if (loadingRef.value) return
  refreshList(1)
}

function strategyCohortLabel() {
  const matched = strategyCohortOptions.find((item) => item.value === strategyCohortRef.value)
  return matched?.label || strategyCohortRef.value || '--'
}

function formatDate(dateValue) {
  const date = new Date(dateValue)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function formatProviderModelLabel(providerName, modelName) {
  const provider = String(providerName || "").trim()
  const model = String(modelName || "").trim()
  if (provider && model) {
    return `${provider} / ${model}`
  }
  return provider || model || '--'
}

function formatDateTime(value) {
  if (!value) return '--'
  return String(value).substring(0, 19).replace('T', ' ')
}

function normalizeNumber(value) {
  const num = Number(value)
  if (Number.isNaN(num) || num <= 0) {
    return 0
  }
  return num
}

function getStockCode(stockCode) {
  let result = String(stockCode || '')
  if (result.indexOf('.') > 0) {
    result = result.split('.')[1] + result.split('.')[0]
  }
  return result.toLowerCase()
}

function extractLineValue(text, prefixes) {
  const content = String(text || '').split('\n')
  for (const line of content) {
    const item = line.trim()
    for (const prefix of prefixes) {
      if (item.startsWith(prefix)) {
        return item.slice(prefix.length).trim()
      }
    }
  }
  return ''
}

function parseEvidenceSources(row) {
  const raw = String(row.evidenceSources || '').trim()
  if (raw) {
    try {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed)) {
        return parsed.filter(item => item && item.type && item.summary).map(item => ({
          type: item.type,
          summary: item.summary,
          sourceName: item.sourceName || '',
          sourceType: item.sourceType || '',
          trustLevel: item.trustLevel || '',
          latencyLevel: item.latencyLevel || '',
          title: item.title || '',
          url: item.url || '',
          publishedAt: item.publishedAt || ''
        }))
      }
    } catch (e) {
      console.error('parse evidenceSources failed', e)
    }
  }
  const keyEvidence = row.keyEvidence || extractLineValue(row.recommendReason, ['关键证据：'])
  if (!keyEvidence) return []
  return String(keyEvidence).split('\n').map(line => {
    const match = line.match(/^\[([^\]]+)\]\s*(.+)$/)
    if (match) {
      return {type: match[1], summary: match[2], sourceName: '', sourceType: '', trustLevel: '', latencyLevel: '', title: '', url: '', publishedAt: ''}
    }
    return {type: '市场资讯', summary: line.trim(), sourceName: '', sourceType: '', trustLevel: '', latencyLevel: '', title: '', url: '', publishedAt: ''}
  }).filter(item => item.summary)
}

function signalPreview(main, detail) {
  const text = [String(main || '').trim(), String(detail || '').trim()].filter(Boolean).join('；')
  return text || '--'
}

function buyBasisPreview(row) {
  return signalPreview(row.buySignal, row.buySignalDetail)
}

function invalidConditionPreview(row) {
  return row.invalidCondition || row.invalidSignal || '--'
}

function showDetail(row) {
  modalDataRef.title = row.stockName
  modalDataRef.stockCode = getStockCode(row.stockCode)
  modalDataRef.stockName = row.stockName || ''
  modalDataRef.coreCatalyst = row.coreCatalyst || extractLineValue(row.recommendReason, ['核心催化：', '核心逻辑：'])
  modalDataRef.keyEvidence = row.keyEvidence || extractLineValue(row.recommendReason, ['关键证据：'])
  modalDataRef.evidenceSources = parseEvidenceSources(row)
  modalDataRef.invalidCondition = row.invalidCondition || extractLineValue(row.recommendReason, ['失效条件：'])
  modalDataRef.observePrice = row.observePrice || row.stockPrice || ''
  modalDataRef.buySignal = row.buySignal || extractLineValue(row.recommendReason, ['买入依据：', '买入信号：'])
  modalDataRef.buySignalDetail = row.buySignalDetail || extractLineValue(row.recommendReason, ['买入补充说明：', '买入补充条件：', '买入条件补充：', '买入信号补充：'])
  modalDataRef.sellSignal = row.sellSignal || extractLineValue(row.recommendReason, ['卖出计划：', '卖出信号：'])
  modalDataRef.sellSignalDetail = row.sellSignalDetail || extractLineValue(row.recommendReason, ['卖出补充说明：', '卖出补充条件：', '卖出条件补充：', '卖出信号补充：'])
  modalDataRef.invalidSignal = row.invalidSignal || extractLineValue(row.recommendReason, ['失效信号：', '失效条件：'])
  modalDataRef.recommendBuyPrice = row.recommendBuyPrice || ''
  modalDataRef.stopProfitPrice = row.recommendStopProfitPrice || ''
  modalDataRef.stopLossPrice = row.recommendStopLossPrice || ''
  modalDataRef.expectedCycle = row.expectedCycle || ''
  modalDataRef.riskRemarks = row.riskRemarks || ''
  modalDataRef.recommendReason = row.recommendReason || ''
  modalDataRef.remarks = row.remarks || ''
  modalDataRef.eventStrength = Number(row.eventStrength || 0)
  modalDataRef.capitalConfirmation = Number(row.capitalConfirmation || 0)
  modalDataRef.fundamentalFit = Number(row.fundamentalFit || 0)
  modalDataRef.technicalFit = Number(row.technicalFit || 0)
  modalDataRef.latestOpeningReview = row.latestOpeningReview || null
  modalDataRef.visible = true
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

function normalizePriceText(value) {
  const text = String(value || '').trim()
  return text || '--'
}

function tradePlanHints(row) {
  const observe = normalizePriceText(row.observePrice || row.stockPrice)
  const buyRange = normalizePriceText(row.recommendBuyPrice)
  const stopProfit = normalizePriceText(row.stopProfitPrice || row.recommendStopProfitPrice)
  const stopLoss = normalizePriceText(row.stopLossPrice || row.recommendStopLossPrice)
  return [
    `价格锚点：${observe}。买入依据里的量能、均线、突破/回踩描述都应围绕这个锚点理解。`,
    `交易计划：买入区间 ${buyRange}，止盈区间 ${stopProfit}，止损位 ${stopLoss}。`,
    `收益率统计只对可执行推荐生效；观察/候选语义不会再直接纳入回测。`,
  ]
}

function deleteAiRecommendStocks(id) {
  DeleteAiRecommendStocks(id).then((res) => {
    notify.info({content: res})
    handleSearch()
  })
}
</script>

<template>
  <n-input-group>
    <n-date-picker v-model:value="researchDateRangeModel" type="daterange" style="width: 50%" />
    <n-select
      v-model:value="strategyCohortRef"
      :options="strategyCohortOptions"
      style="width: 20%"
    />
    <n-input clearable placeholder="输入关键词搜索" v-model:value="paginationReactive.keyword" />
    <n-button type="primary" ghost @click="handleSearch" @input="handleSearch">
      搜索
    </n-button>
  </n-input-group>
  <div style="margin-top: 8px;">
    <n-text depth="3">当前分层：{{ strategyCohortLabel() }}</n-text>
    <n-alert
      v-if="strategyCohortRef === '1.5.0'"
      type="warning"
      :show-icon="false"
      style="margin-top: 8px;"
    >
      V1.5.0 已进入生产推荐，收益结论仍处于前向验证中；请同时关注数据健康告警和真实组合净值。
    </n-alert>
    <n-text depth="3" style="margin-left: 12px;">推荐记录页默认看全量历史；可切换 V1.3.6、V1.3.2 或 V1.3.1 对比不同阶段记录。</n-text>
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
    :row-key="(rowData) => rowData.ID"
    @update:page="handlePageChange"
    flex-height
    style="height: calc(100vh - 210px); margin-top: 10px"
  />

  <n-modal v-model:show="modalDataRef.visible" :title="modalDataRef.title" preset="card" style="width: 960px;">
    <n-gradient-text :size="16" type="warning" style="white-space: pre-wrap; display: block; text-align: left;">
      {{ modalDataRef.remarks || '暂无操作备注' }}
    </n-gradient-text>

    <n-card size="small" style="margin-top: 12px;">
      <KLineChart
        style="width: 900px"
        :code="getStockCode(modalDataRef.stockCode)"
        :chart-height="500"
        :stock-name="modalDataRef.stockName"
        :k-days="30"
        :dark-theme="editorDataRef.darkTheme"
      />
    </n-card>

    <n-card size="small" style="margin-top: 12px; text-align: left;">
      <div style="margin-bottom: 10px; display: flex; flex-wrap: wrap; gap: 8px;">
        <n-tag type="info" :bordered="false">价格锚点：{{ modalDataRef.observePrice || '--' }}</n-tag>
        <n-tag type="primary" :bordered="false">买入区间：{{ modalDataRef.recommendBuyPrice || '--' }}</n-tag>
        <n-tag type="success" :bordered="false">止盈区间：{{ modalDataRef.stopProfitPrice || '--' }}</n-tag>
        <n-tag type="error" :bordered="false">止损位：{{ modalDataRef.stopLossPrice || '--' }}</n-tag>
        <n-tag type="default" :bordered="false">周期：{{ modalDataRef.expectedCycle || '--' }}</n-tag>
      </div>

      <n-alert type="info" :show-icon="false" style="margin-bottom: 12px; text-align: left;">
        <div v-for="(hint, index) in tradePlanHints(modalDataRef)" :key="`price-hint-${index}`" style="line-height: 1.8;">
          {{ hint }}
        </div>
      </n-alert>

      <n-divider><n-gradient-text type="warning">09:40 开盘复核</n-gradient-text></n-divider>
      <n-alert
        v-if="modalDataRef.latestOpeningReview"
        type="warning"
        :show-icon="false"
        style="margin-bottom: 12px; text-align: left;"
      >
        <div style="line-height: 1.8;">
          <div>动作：{{ openingReviewActionText(modalDataRef.latestOpeningReview) }}</div>
          <div>开盘价：{{ modalDataRef.latestOpeningReview.openingPrice || '--' }}，竞价价：{{ modalDataRef.latestOpeningReview.auctionPrice || '--' }}，09:40 最新价：{{ modalDataRef.latestOpeningReview.minutePrice || '--' }}</div>
          <div>原因：{{ modalDataRef.latestOpeningReview.reason || modalDataRef.latestOpeningReview.rawSummary || '--' }}</div>
        </div>
      </n-alert>
      <n-text v-else depth="3" style="white-space: pre-wrap; display: block; text-align: left;">
        该推荐暂未生成 09:40 开盘复核记录。
      </n-text>

      <n-divider><n-gradient-text type="error">买入依据</n-gradient-text></n-divider>
      <n-text type="error" style="white-space: pre-wrap; display: block; text-align: left;">
        {{ modalDataRef.buySignal || '--' }}
      </n-text>
      <n-text v-if="modalDataRef.buySignalDetail" depth="3" style="white-space: pre-wrap; display: block; text-align: left; margin-top: 6px;">
        补充说明：{{ modalDataRef.buySignalDetail }}
      </n-text>

      <n-text type="info" style="white-space: pre-wrap; display: block; text-align: left;">
        核心催化：{{ modalDataRef.coreCatalyst || '--' }}
      </n-text>

      <n-divider><n-gradient-text type="info">关键证据</n-gradient-text></n-divider>
      <div v-if="modalDataRef.evidenceSources.length > 0" style="display: flex; flex-direction: column; gap: 8px;">
        <div v-for="(item, index) in modalDataRef.evidenceSources" :key="`${item.type}-${index}`" style="border-left: 3px solid #18a058; padding-left: 10px;">
          <div style="display: flex; flex-wrap: wrap; gap: 6px; align-items: center;">
            <n-text type="success">[{{ item.type }}]</n-text>
            <n-tag v-if="item.sourceName" size="small" type="info" :bordered="false">{{ item.sourceName }}</n-tag>
            <n-tag v-if="item.sourceType" size="small" type="default" :bordered="false">{{ item.sourceType }}</n-tag>
            <n-tag v-if="item.trustLevel" size="small" :type="item.trustLevel === 'high' ? 'success' : (item.trustLevel === 'medium' ? 'warning' : 'default')" :bordered="false">信任度 {{ item.trustLevel }}</n-tag>
            <n-tag v-if="item.latencyLevel" size="small" type="warning" :bordered="false">{{ item.latencyLevel }}</n-tag>
          </div>
          <n-text v-if="item.title" style="display: block; margin-top: 4px; font-weight: 600; white-space: pre-wrap;">{{ item.title }}</n-text>
          <n-text style="display: block; margin-top: 4px; white-space: pre-wrap;">{{ item.summary }}</n-text>
          <n-text v-if="item.publishedAt || item.url" depth="3" style="display: block; margin-top: 4px; white-space: pre-wrap;">
            {{ [item.publishedAt, item.url].filter(Boolean).join(' ｜ ') }}
          </n-text>
        </div>
      </div>
      <n-text v-else type="info" style="white-space: pre-wrap; display: block; text-align: left;">
        {{ modalDataRef.keyEvidence || '暂无关键证据' }}
      </n-text>

      <n-divider><n-gradient-text type="warning">失效条件</n-gradient-text></n-divider>
      <n-text type="warning" style="white-space: pre-wrap; display: block; text-align: left;">
        {{ modalDataRef.invalidSignal || modalDataRef.invalidCondition || '--' }}
      </n-text>

      <n-divider><n-gradient-text type="info">4维置信度</n-gradient-text></n-divider>
      <n-text style="white-space: pre-wrap; display: block; text-align: left;">
        事件强度：{{ modalDataRef.eventStrength || 0 }} / 资金确认度：{{ modalDataRef.capitalConfirmation || 0 }} / 基本面匹配度：{{ modalDataRef.fundamentalFit || 0 }} / 技术面匹配度：{{ modalDataRef.technicalFit || 0 }}
      </n-text>

      <n-divider><n-gradient-text type="error">风险提示</n-gradient-text></n-divider>
      <n-text type="error" style="white-space: pre-wrap; display: block; text-align: left;">
        {{ modalDataRef.riskRemarks || '--' }}
      </n-text>

      <n-divider><n-gradient-text type="info">兼容字段</n-gradient-text></n-divider>
      <n-text type="info" style="white-space: pre-wrap; display: block; text-align: left;">
        {{ modalDataRef.recommendReason || '--' }}
      </n-text>
    </n-card>
  </n-modal>
</template>

<style scoped>
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
