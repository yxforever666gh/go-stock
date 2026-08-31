<script setup>
import {computed, h, onMounted, ref} from 'vue'
import {NButton, useMessage} from 'naive-ui'
import {GetResearch2Performance, GetResearch2Recommendation, ListResearch2Recommendations} from '../services/research2-api'
import {formatInteger, formatMoney, formatNumber, formatPercent, formatPrice} from '../utils/number-format'
import AppMarkdownPreview from './AppMarkdownPreview.vue'
import ResearchTradeChart from './ResearchTradeChart.vue'

const message = useMessage(), loading = ref(false), performance = ref(null), rows = ref([]), detail = ref(null), visible = ref(false)
const rate = value => value === null || value === undefined ? '--' : formatPercent(value)
const yesNo = value => value === null || value === undefined ? '--' : value ? '是' : '否'
const dateTime = value => value ? String(value).slice(0, 19).replace('T', ' ') : '--'
const assessment = computed(() => !performance.value?.closedTrades ? '暂无已平仓样本' : performance.value.closedTrades < 30 ? '样本不足，仅供观察' : '已有阶段性样本')
const columns = [
  {title: '股票', key: 'stockCode', minWidth: 160, render: row => h(NButton, {text: true, type: 'primary', onClick: () => show(row)}, {default: () => `${row.stockName}（${row.stockCode}）`})},
  {title: '数量', key: 'quantity', width: 90, render: row => formatInteger(row.quantity)},
  {title: '净收益', key: 'netPnl', width: 120, render: row => formatMoney(row.netPnl)},
  {title: '净收益率', key: 'netYieldRate', width: 110, render: row => rate(row.netYieldRate)},
  {title: '卖出前+5%', key: 'hitFiveBeforeSell', width: 110, render: row => yesNo(row.hitFiveBeforeSell)},
  {title: '次日全天涨停', key: 'hitLimitUpFullDay', width: 120, render: row => yesNo(row.hitLimitUpFullDay)},
  {title: '曾低于-3%', key: 'hitMinusThree', width: 110, render: row => yesNo(row.hitMinusThree)},
]
async function show(row) { visible.value = true; detail.value = null; try { detail.value = await GetResearch2Recommendation(row.recommendationId) } catch (error) { message.error(error?.message || String(error)) } }
async function refresh() {
  loading.value = true
  try {
    performance.value = await GetResearch2Performance()
    rows.value = await ListResearch2Recommendations(200, 0) || []
  } catch (error) { message.error(error?.message || String(error)) }
  finally { loading.value = false }
}
onMounted(refresh)
</script>

<template>
  <n-space vertical size="large">
    <n-grid :cols="4" :x-gap="12" :y-gap="12" responsive="screen"><n-gi v-for="item in [
      ['账户净值', formatMoney(performance?.netAssetValue)], ['可用现金', formatMoney(performance?.cash)], ['累计净收益', formatMoney(performance?.netProfit)], ['累计收益率', rate(performance?.returnRate)],
      ['已平仓', `${formatInteger(performance?.closedTrades)} 笔`], ['胜率', rate(performance?.winRate)], ['总费用', formatMoney(performance?.totalFees)], ['最大回撤', rate(performance?.maxDrawdown)],
      ['卖出前+5%', `${formatInteger(performance?.hitFiveCount)} 次`], ['次日全天涨停', `${formatInteger(performance?.hitLimitUpCount)} 次`], ['曾低于-3%', `${formatInteger(performance?.hitMinusThreeCount)} 次`], ['报告时效', `准时 ${formatInteger(performance?.onTimeReports)} / 迟到 ${formatInteger(performance?.lateReports)}`]
    ]" :key="item[0]"><n-card size="small"><n-statistic :label="item[0]" :value="item[1]"/></n-card></n-gi></n-grid>
    <n-alert type="info" :bordered="false">{{assessment}}。主指标为下一交易日10:00卖出后的扣费净收益；+5%、全天涨停和-3%风险分别统计，不混作同一成功标准。</n-alert>
    <n-flex justify="space-between" align="center"><n-text depth="3">账户指标与未平仓收益按最新行情估值；点击股票可查看持仓期分钟走势。</n-text><n-button :loading="loading" @click="refresh">刷新</n-button></n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="920" :row-key="row => row.recommendationId"/>
  </n-space>

  <n-modal v-model:show="visible">
    <n-card class="research-detail-card" title="收益与成交详情" closable @close="visible=false">
      <n-scrollbar style="max-height:87vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <n-descriptions bordered :column="3">
              <n-descriptions-item label="股票">{{detail.recommendation.stockName}}（{{detail.recommendation.stockCode}}）</n-descriptions-item>
              <n-descriptions-item label="净收益">{{formatMoney(detail.recommendation.netPnl)}}</n-descriptions-item>
              <n-descriptions-item label="净收益率">{{rate(detail.recommendation.netYieldRate)}}</n-descriptions-item>
              <n-descriptions-item label="最终分">{{formatNumber(detail.recommendation.finalScore, 1)}}</n-descriptions-item>
              <n-descriptions-item label="状态">{{detail.recommendation.status}}</n-descriptions-item>
              <n-descriptions-item label="信号时间">{{dateTime(detail.recommendation.signalAt)}}</n-descriptions-item>
            </n-descriptions>
            <n-divider title-placement="left">持仓期分钟走势</n-divider>
            <ResearchTradeChart scope="research2" :recommendation-id="detail.recommendation.recommendationId" :fallback-trades="detail.trades || []"/>
            <n-divider title-placement="left">完整报告</n-divider>
            <AppMarkdownPreview :model-value="detail.analysis.reportMarkdown || '暂无报告'"/>
            <n-divider title-placement="left">成交记录</n-divider>
            <n-data-table :data="detail.trades || []" :columns="[
              {title:'方向',key:'side'},
              {title:'时间',key:'tradedAt',render:r=>dateTime(r.tradedAt)},
              {title:'成交价',key:'executionPrice',render:r=>formatPrice(r.executionPrice)},
              {title:'数量',key:'quantity',render:r=>formatInteger(r.quantity)},
              {title:'净现金流',key:'netCashFlow',render:r=>formatMoney(r.netCashFlow)}
            ]"/>
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
