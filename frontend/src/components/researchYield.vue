<script setup>
import {computed, h, onMounted, ref} from 'vue'
import {NButton, NTag, NText, useMessage} from 'naive-ui'
import {MdPreview} from 'md-editor-v3'
import {GetAIRecommendation, GetAISimulatedAccount, ListAIRecommendations} from '../services/research-api'
import StockSparkLine from './stockSparkLine.vue'
import ResearchLifecycleTimeline from './ResearchLifecycleTimeline.vue'

const message = useMessage()
const loading = ref(false)
const account = ref(null)
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)

const positionsByRecommendation = computed(() => new Map((account.value?.positions || []).map(item => [item.recommendationId, item])))
function dateTime(value) { return value ? String(value).slice(0, 19).replace('T', ' ') : '--' }
function money(value) { const number = Number(value || 0); return `${number >= 0 ? '' : '-'}¥${Math.abs(number).toFixed(2)}` }
function percent(value) { const number = Number(value || 0) * 100; return `${number >= 0 ? '+' : ''}${number.toFixed(2)}%` }
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
  {title: '买入时间/价格', key: 'activatedAt', minWidth: 210, render: row => row.activatedAt ? `${dateTime(row.activatedAt)} / ${Number(row.activationPrice).toFixed(3)}` : '--'},
  {title: '数量', key: 'quantity', width: 90},
  {title: '当前或卖出时间/价格', key: 'closedAt', minWidth: 220, render: row => {
    const position = positionsByRecommendation.value.get(row.recommendationId)
    if (row.closedAt) return `${dateTime(row.closedAt)} / ${Number(row.closePrice).toFixed(3)}`
    if (position) return `${dateTime(position.currentPriceAt)} / ${Number(position.currentPrice).toFixed(3)}`
    return '--'
  }},
  {title: '费用', key: 'totalFees', width: 110, render: row => money(rowFees(row))},
  {title: '净收益额', key: 'netPnl', width: 120, render: row => h(NText, {type: colorType(rowNetPnl(row))}, {default: () => money(rowNetPnl(row))})},
  {title: '净收益率', key: 'netYieldRate', width: 120, render: row => h(NText, {type: colorType(rowNetYield(row))}, {default: () => percent(rowNetYield(row))})},
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
      <n-gi><n-statistic label="账户现金" :value="money(account.cash)"/></n-gi>
      <n-gi><n-statistic label="持仓可卖出净值" :value="money(account.positionValue)"/></n-gi>
      <n-gi><n-statistic label="账户净值" :value="money(account.netAssetValue)"/></n-gi>
      <n-gi><n-statistic label="总净收益" :value="money(account.netProfit)"/></n-gi>
      <n-gi><n-statistic label="总净收益率" :value="percent(account.netYieldRate)"/></n-gi>
    </n-grid>
    <n-flex justify="space-between" align="center">
      <n-text depth="3">净收益口径：现金 + 持仓按最新价扣预估卖出成本 − 100,000 元。</n-text>
      <n-button :loading="loading" @click="refresh">刷新估值</n-button>
    </n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="1500" :row-key="row => row.recommendationId"/>
  </n-space>

  <n-modal v-model:show="detailVisible">
    <n-card style="width:min(1180px, 95vw); max-height:92vh" title="收益与成交详情" closable @close="detailVisible=false">
      <n-scrollbar style="max-height:80vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <n-descriptions bordered :column="3">
              <n-descriptions-item label="股票">{{ detail.recommendation.stockName }}（{{ detail.recommendation.stockCode }}）</n-descriptions-item>
              <n-descriptions-item label="净收益">{{ money(detail.position?.netPnl ?? detail.recommendation.netPnl) }}</n-descriptions-item>
              <n-descriptions-item label="净收益率">{{ percent(detail.position?.netYieldRate ?? detail.recommendation.netYieldRate) }}</n-descriptions-item>
              <n-descriptions-item v-if="detail.recommendation.activationCondition" label="旧制历史激活条件" :span="3">{{ detail.recommendation.activationCondition }}</n-descriptions-item>
            </n-descriptions>
            <n-divider>分钟图</n-divider>
            <StockSparkLine :stock-code="detail.recommendation.stockCode" :stock-name="detail.recommendation.stockName" :last-price="detail.position?.currentPrice || detail.recommendation.closePrice" :open-price="detail.recommendation.activationPrice"/>
            <n-divider>完整 AI 报告</n-divider>
            <MdPreview :model-value="detail.analysis.finalReport || '暂无报告'"/>
            <n-divider>交易与 AI 判断时间线</n-divider>
            <ResearchLifecycleTimeline :detail="detail"/>
            <n-divider>成交记录</n-divider>
            <n-data-table :columns="[
              {title:'方向',key:'side'}, {title:'时间',key:'tradedAt',render:r=>dateTime(r.tradedAt)}, {title:'市场价',key:'marketPrice'},
              {title:'成交价',key:'executionPrice'}, {title:'数量',key:'quantity'}, {title:'佣金',key:'commission'},
              {title:'印花税',key:'stampDuty'}, {title:'过户费',key:'transferFee'}, {title:'滑点',key:'slippageAmount'}, {title:'净现金流',key:'netCashFlow'}
            ]" :data="detail.trades || []" :scroll-x="1100"/>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>
