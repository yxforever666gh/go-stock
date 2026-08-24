<script setup>
import {computed, h, onMounted, ref} from 'vue'
import {NButton, NTag, NText, useMessage} from 'naive-ui'
import {
  GetAIRecommendation,
  GetAISimulatedAccount,
  GetAISimulatedAccountPerformance,
  ListAIRecommendations,
  ListAISimulatedAccountCashFlows,
} from '../services/research-api'
import {formatInteger, formatMoney, formatNumber, formatPercent, formatPrice} from '../utils/number-format'
import {
  formatHoldingMinutes,
  normalizeAccountOverview,
  normalizeCashFlows,
  normalizePerformance,
} from '../utils/research-performance'
import AppMarkdownPreview from './AppMarkdownPreview.vue'
import ResearchLifecycleTimeline from './ResearchLifecycleTimeline.vue'
import ResearchTradeChart from './ResearchTradeChart.vue'

const message = useMessage()
const loading = ref(false)
const account = ref(null)
const performance = ref(null)
const cashFlows = ref([])
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)

const positionsByRecommendation = computed(() => new Map((account.value?.positions || []).map(item => [item.recommendationId, item])))
const performanceMetrics = computed(() => performance.value?.metrics || normalizePerformance().metrics)

function dateTime(value) { return value ? String(value).slice(0, 19).replace('T', ' ') : '--' }
function dateOnly(value) { return value ? String(value).slice(0, 10) : '--' }
function colorType(value) { return Number(value || 0) >= 0 ? 'error' : 'success' }
function positionFor(row) { return positionsByRecommendation.value.get(row.recommendationId) }
function rowFees(row) { const position = positionFor(row); return position ? Number(position.buyFees || 0) + Number(position.estimatedSellFees || 0) : Number(row.totalFees || 0) }
function rowNetPnl(row) { return positionFor(row)?.netPnl ?? row.netPnl }
function rowNetYield(row) { return positionFor(row)?.netYieldRate ?? row.netYieldRate }
function optionalMoney(value) { return value === null || value === undefined ? '--' : formatMoney(value) }
function optionalPercent(value) { return value === null || value === undefined ? '--' : formatPercent(value) }
function optionalUnsignedPercent(value) { return value === null || value === undefined ? '--' : `${formatNumber(Math.abs(Number(value)) * 100, 2)}%` }
function optionalNumber(value, digits = 2) { return value === null || value === undefined ? '--' : formatNumber(value, digits) }

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

const cashFlowColumns = [
  {title: '类型', key: 'type', width: 130, render: row => row.type === 'initial_deposit' ? '初始入金' : row.type === 'scheduled_deposit' ? '计划入金' : row.type},
  {title: '交易日', key: 'tradingDate', width: 120, render: row => dateOnly(row.tradingDate)},
  {title: '生效时间', key: 'effectiveAt', width: 180, render: row => dateTime(row.effectiveAt)},
  {title: '金额', key: 'amount', width: 150, render: row => formatMoney(row.amount)},
  {title: '入金前净值', key: 'netAssetValueBefore', width: 150, render: row => optionalMoney(row.netAssetValueBefore)},
  {title: '入金后净值', key: 'netAssetValueAfter', width: 150, render: row => optionalMoney(row.netAssetValueAfter)},
  {title: '入金前单位净值', key: 'unitValueBefore', width: 150, render: row => optionalNumber(row.unitValueBefore, 6)},
  {title: '新增份额', key: 'unitsIssued', width: 135, render: row => optionalNumber(row.unitsIssued, 4)},
]

const curveColumns = [
  {title: '估值时间', key: 'valuedAt', width: 180, render: row => dateTime(row.valuedAt)},
  {title: '交易日', key: 'tradingDate', width: 120, render: row => dateOnly(row.tradingDate)},
  {title: '快照类型', key: 'snapshotType', width: 130, render: row => ({pre_deposit: '入金前', post_deposit: '入金后', daily_close: '收盘', current: '当前'}[row.snapshotType] || row.snapshotType)},
  {title: '现金', key: 'cash', width: 145, render: row => formatMoney(row.cash)},
  {title: '持仓净值', key: 'positionValue', width: 145, render: row => formatMoney(row.positionValue)},
  {title: '账户净值', key: 'netAssetValue', width: 145, render: row => formatMoney(row.netAssetValue)},
  {title: '累计投入', key: 'cumulativeNetContribution', width: 145, render: row => formatMoney(row.cumulativeNetContribution)},
  {title: '单位净值', key: 'unitValue', width: 125, render: row => formatNumber(row.unitValue, 6)},
  {title: 'TWR', key: 'timeWeightedReturn', width: 115, render: row => h(NText, {type: colorType(row.timeWeightedReturn)}, {default: () => formatPercent(row.timeWeightedReturn)})},
]

async function refresh() {
  loading.value = true
  try {
    const [accountResult, recommendationResult, cashFlowResult] = await Promise.allSettled([
      GetAISimulatedAccount(),
      ListAIRecommendations(200, 0),
      ListAISimulatedAccountCashFlows(),
    ])
    if (accountResult.status === 'rejected') throw accountResult.reason
    const normalizedAccount = normalizeAccountOverview(accountResult.value || {})
    account.value = normalizedAccount
    rows.value = recommendationResult.status === 'fulfilled'
      ? (recommendationResult.value || []).filter(item => item.activatedAt || ['missed_cash', 'missed_untradable'].includes(item.status))
      : []
    cashFlows.value = normalizeCashFlows(cashFlowResult.status === 'fulfilled' ? cashFlowResult.value : [])
    let performanceResult
    try { performanceResult = {status: 'fulfilled', value: await GetAISimulatedAccountPerformance()} }
    catch (reason) { performanceResult = {status: 'rejected', reason} }
    performance.value = normalizePerformance(performanceResult.status === 'fulfilled' ? performanceResult.value : {}, normalizedAccount)
    const optionalFailures = [recommendationResult, cashFlowResult, performanceResult].filter(item => item.status === 'rejected')
    if (optionalFailures.length) message.warning(`部分评估数据暂不可用（${optionalFailures.length} 项），已展示当前账户数据`)
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
  <n-space vertical size="large">
    <section v-if="account" class="account-overview-grid">
      <n-card size="small" class="primary-metric-card">
        <n-statistic label="策略净收益率（TWR）" :value="formatPercent(performance?.timeWeightedReturn ?? account.timeWeightedReturn)"/>
        <n-text depth="3" class="metric-note">基于固定 50 万元初始资金</n-text>
      </n-card>
      <n-card size="small"><n-statistic label="账户净值" :value="formatMoney(account.netAssetValue)"/><n-text depth="3" class="metric-note">估值 {{ dateTime(performance?.valuedAt || account.valuedAt) }}</n-text></n-card>
      <n-card size="small"><n-statistic label="净收益额" :value="formatMoney(performance?.netProfit ?? account.netProfit)"/><n-text depth="3" class="metric-note">净值减固定初始资金</n-text></n-card>
      <n-card size="small"><n-statistic label="累计投入回报率" :value="formatPercent(performance?.cumulativeCapitalReturn ?? account.cumulativeCapitalReturn)"/><n-text depth="3" class="metric-note">净收益额 ÷ 500,000 元</n-text></n-card>
      <n-card size="small"><n-statistic label="账户现金" :value="formatMoney(account.cash)"/><n-text depth="3" class="metric-note">持仓可卖出净值 {{ formatMoney(account.positionValue) }}</n-text></n-card>
      <n-card size="small"><n-statistic label="单位净值" :value="formatNumber(performance?.unitValue ?? 1, 6)"/><n-text depth="3" class="metric-note">用于时间加权收益计算</n-text></n-card>
    </section>

    <n-card size="small" title="策略评估">
      <template #header-extra>
        <n-tag :type="performanceMetrics.sampleAssessmentType" :bordered="false">{{ performanceMetrics.sampleAssessment }}</n-tag>
      </template>
      <div class="strategy-metrics-grid">
        <div class="strategy-metric"><span>已平仓样本</span><strong>{{ formatInteger(performanceMetrics.closedTrades) }} 笔</strong></div>
        <div class="strategy-metric"><span>胜率</span><strong>{{ optionalUnsignedPercent(performanceMetrics.winRate) }}</strong></div>
        <div class="strategy-metric"><span>平均盈利率</span><strong>{{ optionalPercent(performanceMetrics.averageGainRate) }}</strong></div>
        <div class="strategy-metric"><span>平均亏损率</span><strong>{{ optionalPercent(performanceMetrics.averageLossRate) }}</strong></div>
        <div class="strategy-metric"><span>盈亏比</span><strong>{{ optionalNumber(performanceMetrics.payoffRatio) }}</strong></div>
        <div class="strategy-metric"><span>最大回撤</span><strong>{{ optionalUnsignedPercent(performanceMetrics.maxDrawdown) }}</strong></div>
        <div class="strategy-metric"><span>总费用</span><strong>{{ optionalMoney(performanceMetrics.totalFees) }}</strong></div>
        <div class="strategy-metric"><span>换手率</span><strong>{{ optionalUnsignedPercent(performanceMetrics.turnoverRate) }}</strong></div>
        <div class="strategy-metric"><span>资金利用率</span><strong>{{ optionalUnsignedPercent(performanceMetrics.capitalUtilization) }}</strong></div>
        <div class="strategy-metric"><span>平均持有时间</span><strong>{{ formatHoldingMinutes(performanceMetrics.averageHoldingMinutes) }}</strong></div>
        <div class="strategy-metric"><span>错过成交率</span><strong>{{ optionalUnsignedPercent(performanceMetrics.missedExecutionRate) }}</strong></div>
        <div class="strategy-metric"><span>行业集中度</span><strong>{{ performanceMetrics.industryConcentration === null ? '暂无结构化行业数据' : optionalUnsignedPercent(performanceMetrics.industryConcentration) }}</strong></div>
      </div>
      <n-text depth="3" class="assessment-note">少于 30 笔为样本不足，30–99 笔仅作初步观察，达到 100 笔后再进行阶段性评价。行业集中度会在形成可靠的结构化行业归属后启用，不从 AI 自由文本猜测。</n-text>
    </n-card>

    <n-collapse arrow-placement="right">
      <n-collapse-item title="资金流水" name="cash-flows">
        <n-data-table :columns="cashFlowColumns" :data="cashFlows" :loading="loading" :scroll-x="1190" :row-key="row => row.flowId"/>
      </n-collapse-item>
      <n-collapse-item title="账户净值与 TWR 数据" name="performance-curve">
        <n-data-table :columns="curveColumns" :data="performance?.curve || []" :loading="loading" :scroll-x="1250" :row-key="(row, index) => `${row.valuedAt}-${row.snapshotType}-${index}`"/>
      </n-collapse-item>
    </n-collapse>

    <n-flex justify="space-between" align="center">
      <n-text depth="3">净收益额 = 账户净值 − 固定 500,000 元初始资金；账户不再追加注资。</n-text>
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
.account-overview-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 12px;
}

.primary-metric-card {
  border-color: rgba(24, 160, 88, 0.45);
  background: linear-gradient(145deg, rgba(24, 160, 88, 0.09), transparent 70%);
}

.metric-note,
.assessment-note {
  display: block;
  margin-top: 6px;
  font-size: 12px;
}

.strategy-metric {
  border: 1px solid var(--n-border-color);
  border-radius: 6px;
  padding: 10px 12px;
  min-width: 0;
}

.strategy-metric span {
  display: block;
  color: var(--n-text-color-3);
  font-size: 12px;
  margin-bottom: 4px;
}

.strategy-metric strong {
  font-size: 16px;
  font-weight: 600;
  overflow-wrap: anywhere;
}

.strategy-metrics-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(145px, 1fr));
  gap: 10px;
}

.research-detail-card {
  width: min(1600px, 96vw);
  max-height: 96vh;
}

@media (max-width: 560px) {
  .strategy-metrics-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
