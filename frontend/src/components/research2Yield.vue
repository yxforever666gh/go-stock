<script setup>
import {computed, onMounted, ref} from 'vue'
import {useMessage} from 'naive-ui'
import {GetResearch2Performance, ListResearch2Recommendations} from '../services/research2-api'
import {formatInteger, formatMoney, formatPercent} from '../utils/number-format'

const message = useMessage(), loading = ref(false), performance = ref(null), rows = ref([])
const rate = value => value === null || value === undefined ? '--' : formatPercent(value)
const yesNo = value => value === null || value === undefined ? '--' : value ? '是' : '否'
const assessment = computed(() => !performance.value?.closedTrades ? '暂无已平仓样本' : performance.value.closedTrades < 30 ? '样本不足，仅供观察' : '已有阶段性样本')
const columns = [
  {title: '股票', key: 'stockCode', minWidth: 160, render: row => `${row.stockName}（${row.stockCode}）`},
  {title: '数量', key: 'quantity', width: 90, render: row => formatInteger(row.quantity)},
  {title: '净收益', key: 'netPnl', width: 120, render: row => formatMoney(row.netPnl)},
  {title: '净收益率', key: 'netYieldRate', width: 110, render: row => rate(row.netYieldRate)},
  {title: '卖出前+5%', key: 'hitFiveBeforeSell', width: 110, render: row => yesNo(row.hitFiveBeforeSell)},
  {title: '次日全天涨停', key: 'hitLimitUpFullDay', width: 120, render: row => yesNo(row.hitLimitUpFullDay)},
  {title: '曾低于-3%', key: 'hitMinusThree', width: 110, render: row => yesNo(row.hitMinusThree)},
]
async function refresh() { loading.value = true; try { [performance.value, rows.value] = await Promise.all([GetResearch2Performance(), ListResearch2Recommendations(200, 0)]) } catch (error) { message.error(error?.message || String(error)) } finally { loading.value = false } }
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
    <n-flex justify="end"><n-button :loading="loading" @click="refresh">刷新</n-button></n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="920" :row-key="row => row.recommendationId"/>
  </n-space>
</template>
