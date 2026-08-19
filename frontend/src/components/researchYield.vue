<script setup>
import {computed, h, onMounted, ref} from 'vue'
import {NButton, NTag, NText, useMessage} from 'naive-ui'
import {GetAIRecommendation, GetAISimulatedAccount, ListAIRecommendations} from '../services/research-api'
import {formatInteger, formatMoney, formatPercent, formatPrice} from '../utils/number-format'
import AppMarkdownPreview from './AppMarkdownPreview.vue'
import ResearchLifecycleTimeline from './ResearchLifecycleTimeline.vue'
import ResearchTradeChart from './ResearchTradeChart.vue'

const message = useMessage()
const loading = ref(false)
const account = ref(null)
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)

const positionsByRecommendation = computed(() => new Map((account.value?.positions || []).map(item => [item.recommendationId, item])))
function dateTime(value) { return value ? String(value).slice(0, 19).replace('T', ' ') : '--' }
function colorType(value) { return Number(value || 0) >= 0 ? 'error' : 'success' }
function positionFor(row) { return positionsByRecommendation.value.get(row.recommendationId) }
function rowFees(row) { const position = positionFor(row); return position ? Number(position.buyFees || 0) + Number(position.estimatedSellFees || 0) : Number(row.totalFees || 0) }
function rowNetPnl(row) { return positionFor(row)?.netPnl ?? row.netPnl }
function rowNetYield(row) { return positionFor(row)?.netYieldRate ?? row.netYieldRate }
const statusLabels = {buy_pending: '待买入', pending: '旧制待激活', active: '持仓中', sell_pending: '待卖出', invalidated: '旧制已失效', missed_cash: '错过—资金不足', missed_untradable: '错过—不可交易', closed: '已卖出'}
function statusType(status) { if (status === 'active') return 'success'; if (status === 'closed') return 'info'; if (status === 'buy_pending' || status === 'pending' || status === 'sell_pending') return 'warning'; return 'error' }

const columns = [
  {title: '股票名称', key: 'stockName', width: 120, render: row => h(NButton, {text: true, type: 'primary', onClick: () => showDetail(row)}, {default: () => row.stockName})},
  {title: '代码', key: 'stockCode', width: 110},
  {title: '信号时间', key: 'signalAt', width: 170, render: row => dateTime(row.signalAt)},
  {title: '交易状态', key: 'status', width: 145, render: row => h(NTag, {type: statusType(row.status), bordered: false}, {default: () => statusLabels[row.status] || row.status})},
  {title: '买入时间/价格', key: 'activatedAt', minWidth: 210, render: row => row.activatedAt ? `${dateTime(row.activatedAt)} / ${formatPrice(row.activationPrice)}` : '--'},
  {title: '数量', key: 'quantity', width: 100, render: row => row.activatedAt ? formatInteger(row.quantity) : '--'},
  {title: '当前或卖出时间/价格', key: 'closedAt', minWidth: 220, render: row => {
    const position = positionsByRecommendation.value.get(row.recommendationId)
    if (row.closedAt) return `${dateTime(row.closedAt)} / ${formatPrice(row.closePrice)}`
    if (position) return `${dateTime(position.currentPriceAt)} / ${formatPrice(position.currentPrice)}`
    return '--'
  }},
  {title: '费用', key: 'totalFees', width: 120, render: row => formatMoney(rowFees(row))},
  {title: '净收益额', key: 'netPnl', width: 135, render: row => h(NText, {type: colorType(rowNetPnl(row))}, {default: () => formatMoney(rowNetPnl(row))})},
  {title: '净收益率', key: 'netYieldRate', width: 120, render: row => h(NText, {type: colorType(rowNetYield(row))}, {default: () => formatPercent(rowNetYield(row))})},
]

async function refresh() {
  loading.value = true
  try {
    account.value = await GetAISimulatedAccount()
    const recommendationResult = await ListAIRecommendations(200, 0)
    rows.value = (recommendationResult || []).filter(item => item.activatedAt || ['missed_cash', 'missed_untradable'].includes(item.status))
  } catch (error) { message.error(error?.message || String(error)) }
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
    <n-grid :cols="5" :x-gap="12" v-if="account">
      <n-gi><n-statistic label="账户现金" :value="formatMoney(account.cash)"/></n-gi>
      <n-gi><n-statistic label="持仓可卖出净值" :value="formatMoney(account.positionValue)"/></n-gi>
      <n-gi><n-statistic label="账户净值" :value="formatMoney(account.netAssetValue)"/></n-gi>
      <n-gi><n-statistic label="总净收益" :value="formatMoney(account.netProfit)"/></n-gi>
      <n-gi><n-statistic label="总净收益率" :value="formatPercent(account.netYieldRate)"/></n-gi>
    </n-grid>
    <n-flex justify="space-between" align="center">
      <n-text depth="3">净收益口径：现金 + 持仓按最新价扣预估卖出成本 − 100,000 元。</n-text>
      <n-button :loading="loading" @click="refresh">刷新估值</n-button>
    </n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="1500" :row-key="row => row.recommendationId"/>
  </n-space>

  <n-modal v-model:show="detailVisible">
    <n-card class="research-detail-card" title="收益与成交详情" closable @close="detailVisible=false">
      <n-scrollbar style="max-height:87vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <n-descriptions bordered :column="3">
              <n-descriptions-item label="股票">{{ detail.recommendation.stockName }}（{{ detail.recommendation.stockCode }}）</n-descriptions-item>
              <n-descriptions-item label="净收益">{{ formatMoney(detail.position?.netPnl ?? detail.recommendation.netPnl) }}</n-descriptions-item>
              <n-descriptions-item label="净收益率">{{ formatPercent(detail.position?.netYieldRate ?? detail.recommendation.netYieldRate) }}</n-descriptions-item>
              <n-descriptions-item v-if="detail.recommendation.activationCondition" label="旧制历史激活条件" :span="3">{{ detail.recommendation.activationCondition }}</n-descriptions-item>
            </n-descriptions>
            <n-divider title-placement="left">持仓期分钟走势</n-divider>
            <ResearchTradeChart :recommendation-id="detail.recommendation.recommendationId" :fallback-trades="detail.trades || []"/>
            <n-divider>完整 AI 报告</n-divider>
            <AppMarkdownPreview :model-value="detail.analysis.finalReport || '暂无报告'"/>
            <n-divider>交易与 AI 判断时间线</n-divider>
            <ResearchLifecycleTimeline :detail="detail"/>
            <n-divider>成交记录</n-divider>
            <n-data-table :columns="[
              {title:'方向',key:'side'}, {title:'时间',key:'tradedAt',render:r=>dateTime(r.tradedAt)},
              {title:'市场价',key:'marketPrice',render:r=>formatPrice(r.marketPrice)},
              {title:'成交价',key:'executionPrice',render:r=>formatPrice(r.executionPrice)},
              {title:'数量',key:'quantity',render:r=>formatInteger(r.quantity)},
              {title:'佣金',key:'commission',render:r=>formatMoney(r.commission)},
              {title:'印花税',key:'stampDuty',render:r=>formatMoney(r.stampDuty)},
              {title:'过户费',key:'transferFee',render:r=>formatMoney(r.transferFee)},
              {title:'滑点',key:'slippageAmount',render:r=>formatMoney(r.slippageAmount)},
              {title:'净现金流',key:'netCashFlow',render:r=>formatMoney(r.netCashFlow)}
            ]" :data="detail.trades || []" :scroll-x="1100"/>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>

<style scoped>
.research-detail-card {
  width: min(1600px, 96vw);
  max-height: 96vh;
}
</style>
