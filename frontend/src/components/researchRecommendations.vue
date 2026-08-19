<script setup>
import {h, onMounted, ref} from 'vue'
import {NButton, NTag, NText, useMessage} from 'naive-ui'
import {GetAIRecommendation, GetAISimulatedAccount, ListAIRecommendations} from '../services/research-api'
import {useDraggableDataTableColumns} from '../composables/useDraggableDataTableColumns'
import {formatInteger, formatMoney, formatPercent, formatPrice} from '../utils/number-format'
import AppMarkdownPreview from './AppMarkdownPreview.vue'
import ResearchLifecycleTimeline from './ResearchLifecycleTimeline.vue'
import ResearchTradeChart from './ResearchTradeChart.vue'

const message = useMessage()
const loading = ref(false)
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)

const statusLabels = {buy_pending: '待买入', pending: '旧制待激活', active: '持仓中', sell_pending: '待卖出', invalidated: '旧制已失效', missed_cash: '错过—资金不足', missed_untradable: '错过—不可交易', closed: '已卖出'}
function dateTime(value) { return value ? String(value).slice(0, 19).replace('T', ' ') : '--' }
function hasBuy(row) { return Boolean(row.activatedAt) && Number(row.buyAmount || 0) > 0 }
function colorType(value) { return Number(value || 0) >= 0 ? 'error' : 'success' }
function statusType(status) { if (status === 'active') return 'success'; if (status === 'closed') return 'info'; if (status === 'buy_pending' || status === 'pending' || status === 'sell_pending') return 'warning'; return 'error' }

const defaultColumns = [
  {title: '股票名称', key: 'stockName', width: 120, render: row => h(NButton, {text: true, type: 'primary', onClick: () => showDetail(row)}, {default: () => row.stockName})},
  {title: '股票代码', key: 'stockCode', width: 115},
  {title: '信号时间', key: 'signalAt', width: 170, render: row => dateTime(row.signalAt)},
  {title: 'AI 摘要', key: 'aiSummary', minWidth: 260, ellipsis: {tooltip: true}},
  {title: '主要风险', key: 'mainRisk', minWidth: 220, ellipsis: {tooltip: true}},
  {title: '当前状态', key: 'status', width: 135, render: row => h(NTag, {type: statusType(row.status), bordered: false}, {default: () => statusLabels[row.status] || row.status})},
  {title: '买入金额', key: 'buyAmount', width: 140, render: row => hasBuy(row) ? formatMoney(row.buyAmount) : '--'},
  {title: '卖出金额', key: 'sellAmount', width: 140, render: row => Number(row.sellAmount || 0) > 0 ? formatMoney(row.sellAmount) : '--'},
  {title: '当前金额', key: 'currentAmount', width: 140, render: row => Number(row.currentAmount || 0) > 0 ? formatMoney(row.currentAmount) : '--'},
  {title: '净收益率', key: 'netYieldRate', width: 120, render: row => hasBuy(row) ? h(NText, {type: colorType(row.netYieldRate)}, {default: () => formatPercent(row.netYieldRate)}) : '--'},
  {title: '来源', key: 'sourceRefs', minWidth: 150, ellipsis: {tooltip: true}},
]
const {tableRef, columnsRef} = useDraggableDataTableColumns(defaultColumns, 'go-stock:research-recommendations:column-order:v1')

async function refresh() {
  loading.value = true
  try {
    await GetAISimulatedAccount()
    rows.value = await ListAIRecommendations(200, 0) || []
  }
  catch (error) { message.error(error?.message || String(error)) }
  finally { loading.value = false }
}

async function showDetail(row) {
  detailVisible.value = true
  detail.value = null
  try { detail.value = await GetAIRecommendation(row.recommendationId) }
  catch (error) { message.error(error?.message || String(error)) }
}

onMounted(refresh)
</script>

<template>
  <n-space vertical>
    <n-flex justify="space-between" align="center">
      <n-text depth="3">金额均为净现金口径；拖动表头可左右调整列顺序并自动保存。点击股票名称查看独立会话和成交详情。</n-text>
      <n-button :loading="loading" @click="refresh">刷新</n-button>
    </n-flex>
    <div ref="tableRef">
      <n-data-table :columns="columnsRef" :data="rows" :loading="loading" :scroll-x="1890" :row-key="row => row.recommendationId"/>
    </div>
  </n-space>

  <n-modal v-model:show="detailVisible">
    <n-card class="research-detail-card" title="股票推荐详情" closable @close="detailVisible = false">
      <n-scrollbar style="max-height:87vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <n-descriptions bordered :column="3" size="small">
              <n-descriptions-item label="股票">{{ detail.recommendation.stockName }}（{{ detail.recommendation.stockCode }}）</n-descriptions-item>
              <n-descriptions-item label="信号时间">{{ dateTime(detail.recommendation.signalAt) }}</n-descriptions-item>
              <n-descriptions-item label="状态">{{ statusLabels[detail.recommendation.status] || detail.recommendation.status }}</n-descriptions-item>
              <n-descriptions-item v-if="detail.recommendation.activationCondition" label="旧制历史激活条件" :span="3">{{ detail.recommendation.activationCondition }}</n-descriptions-item>
              <n-descriptions-item label="主要风险" :span="3">{{ detail.recommendation.mainRisk }}</n-descriptions-item>
            </n-descriptions>
            <n-divider title-placement="left">持仓期分钟走势</n-divider>
            <ResearchTradeChart :recommendation-id="detail.recommendation.recommendationId" :fallback-trades="detail.trades || []"/>
            <n-divider title-placement="left">完整 AI 报告</n-divider>
            <AppMarkdownPreview :model-value="detail.analysis.finalReport || '暂无报告'"/>
            <n-divider title-placement="left">交易与 AI 判断时间线</n-divider>
            <ResearchLifecycleTimeline :detail="detail"/>
            <n-divider title-placement="left">成交与净收益</n-divider>
            <n-data-table :columns="[
              {title:'方向', key:'side'}, {title:'成交时间', key:'tradedAt', render:r=>dateTime(r.tradedAt)},
              {title:'成交价', key:'executionPrice', render:r=>formatPrice(r.executionPrice)},
              {title:'数量', key:'quantity', render:r=>formatInteger(r.quantity)},
              {title:'费用', key:'totalFees', render:r=>formatMoney(r.totalFees)},
              {title:'净现金流', key:'netCashFlow', render:r=>formatMoney(r.netCashFlow)}
            ]" :data="detail.trades || []"/>
            <n-alert v-if="detail.position" type="info" style="margin-top:12px">
              数量 {{ formatInteger(detail.position.quantity) }}，买入价 {{ formatPrice(detail.position.entryPrice) }}，净收益 {{ formatMoney(detail.position.netPnl) }}
            </n-alert>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>

<style scoped>
:deep(.draggable-column-title) {
  display: inline-flex;
  width: 100%;
  cursor: grab;
  user-select: none;
}

:deep(.draggable-column-title.column-dragging) {
  opacity: 0.55;
}

:deep(.draggable-column-title.column-drag-over) {
  box-shadow: inset 3px 0 0 #18a058;
}

.research-detail-card {
  width: min(1600px, 96vw);
  max-height: 96vh;
}
</style>
