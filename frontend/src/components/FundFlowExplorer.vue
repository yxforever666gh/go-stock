<script setup>
import * as echarts from 'echarts'
import {NText} from 'naive-ui'
import {computed, h, nextTick, onBeforeUnmount, onMounted, ref, toRef, watch} from 'vue'
import {GetMarketFundFlows} from '../services/market-api.js'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import {dateValue, historyFrom, itemCode, itemName, numberValue, rowsFrom} from '../market-tabs/market-data.js'
import {shanghaiDate} from '../market-tabs/market-session.js'
import EvidenceStatusBar from './EvidenceStatusBar.vue'

const props = defineProps({
  active: {type: Boolean, default: false},
  darkTheme: {type: Boolean, default: false},
  scope: {type: String, required: true, validator: value => ['sector', 'concept'].includes(value)},
})

const active = toRef(props, 'active')
const selectedDate = ref(shanghaiDate())
const sort = ref('netamount')
const limit = ref(100)
const requestKey = computed(() => ['fund-flow', props.scope, selectedDate.value, sort.value, limit.value].join('|'))
const selectedCodes = ref([])
const chartElement = ref(null)
let chart = null
let resizeObserver = null

const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {rows: []},
  intervalMs: 60000,
  loader: () => GetMarketFundFlows({scope: props.scope, date: selectedDate.value, sort: sort.value, limit: limit.value}),
  requestKey,
})

const rows = computed(() => rowsFrom(data.value).map((row, index) => ({
  ...row,
  _key: itemCode(row, index),
  _name: itemName(row),
  _netInflow: numberValue(row, ['netAmount', 'netInflow', 'net_inflow', 'mainNetInflow', 'main_net_inflow']),
  _inAmount: numberValue(row, ['inAmount', 'inflow', 'in_amount']),
  _outAmount: numberValue(row, ['outAmount', 'outflow', 'out_amount']),
  _changePercent: numberValue(row, ['changePercent', 'changePct', 'change_rate', 'pctChange']),
})))

const columns = computed(() => [
  {type: 'selection', multiple: true, width: 42},
  {title: '代码', key: '_key', width: 110},
  {title: props.scope === 'sector' ? '板块' : '概念', key: '_name', minWidth: 150, ellipsis: {tooltip: true}},
  {
    title: '净流入', key: '_netInflow', width: 130, sorter: (left, right) => left._netInflow - right._netInflow,
    render: row => h(NText, {type: row._netInflow >= 0 ? 'error' : 'success'}, {default: () => formatAmount(row._netInflow)}),
  },
  {title: '流入', key: '_inAmount', width: 130, sorter: (left, right) => left._inAmount - right._inAmount, render: row => formatAmount(row._inAmount)},
  {title: '流出', key: '_outAmount', width: 130, sorter: (left, right) => left._outAmount - right._outAmount, render: row => formatAmount(row._outAmount)},
  {
    title: '涨跌幅', key: '_changePercent', width: 110, sorter: (left, right) => left._changePercent - right._changePercent,
    render: row => h(NText, {type: row._changePercent >= 0 ? 'error' : 'success'}, {default: () => `${row._changePercent >= 0 ? '+' : ''}${row._changePercent.toFixed(2)}%`}),
  },
])

const selectedRows = computed(() => {
  const selected = new Set(selectedCodes.value)
  return rows.value.filter(row => selected.has(row._key))
})

function formatAmount(value) {
  const number = Number(value)
  if (!Number.isFinite(number)) return '--'
  if (Math.abs(number) >= 100000000) return `${(number / 100000000).toFixed(2)} 亿`
  if (Math.abs(number) >= 10000) return `${(number / 10000).toFixed(2)} 万`
  return number.toFixed(2)
}

function pointValue(point) {
  return numberValue(point, ['netAmount', 'netInflow', 'net_inflow', 'value', 'amount'])
}

function renderChart() {
  if (!chart || chart.isDisposed()) return
  const allTimes = [...new Set(selectedRows.value.flatMap(row => historyFrom(row).map(dateValue)).filter(Boolean))].sort()
  if (!allTimes.length) {
    chart.clear()
    return
  }
  const textColor = props.darkTheme ? '#c8c8c8' : '#4b5563'
  const series = selectedRows.value.map(row => {
    const values = new Map(historyFrom(row).map(point => [dateValue(point), pointValue(point)]))
    return {name: row._name, type: 'line', showSymbol: false, smooth: true, data: allTimes.map(time => values.get(time) ?? null)}
  })
  chart.setOption({
    animation: false,
    tooltip: {trigger: 'axis', confine: true, axisPointer: {type: 'cross'}},
    legend: {type: 'scroll', top: 0, textStyle: {color: textColor}},
    grid: {left: 72, right: 34, top: 54, bottom: 54},
    xAxis: {type: 'category', boundaryGap: false, data: allTimes, axisLabel: {color: textColor, hideOverlap: true}},
    yAxis: {type: 'value', scale: true, axisLabel: {color: textColor, formatter: formatAmount}, splitLine: {lineStyle: {type: 'dashed', opacity: 0.35}}},
    dataZoom: [{type: 'inside', start: 0, end: 100}, {type: 'slider', height: 20, bottom: 8}],
    series,
  }, true)
}

watch(rows, nextRows => {
  const available = new Set(nextRows.map(row => row._key))
  const retained = selectedCodes.value.filter(code => available.has(code))
  if (retained.length) selectedCodes.value = retained
  else {
    const inflow = [...nextRows].filter(row => row._netInflow >= 0).sort((a, b) => b._netInflow - a._netInflow).slice(0, 3)
    const outflow = [...nextRows].filter(row => row._netInflow < 0).sort((a, b) => a._netInflow - b._netInflow).slice(0, 3)
    selectedCodes.value = [...inflow, ...outflow].map(row => row._key)
  }
}, {deep: true})
watch([selectedRows, () => props.darkTheme], async () => { await nextTick(); renderChart() }, {deep: true})
onMounted(() => {
  if (!chartElement.value) return
  chart = echarts.init(chartElement.value)
  resizeObserver = new ResizeObserver(() => chart?.resize())
  resizeObserver.observe(chartElement.value)
  renderChart()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  if (chart && !chart.isDisposed()) chart.dispose()
  chart = null
})
</script>

<template>
  <section class="fund-flow-explorer">
    <n-flex align="center" :wrap="true" class="flow-toolbar">
      <n-date-picker v-model:formatted-value="selectedDate" type="date" value-format="yyyy-MM-dd" :is-date-disabled="ts => ts > Date.now()" style="width: 150px"/>
      <n-select v-model:value="sort" :options="[{label:'净流入',value:'netamount'},{label:'流入',value:'inamount'},{label:'流出',value:'outamount'},{label:'平均涨跌幅',value:'avg_changeratio'}]" style="width: 140px"/>
      <n-select v-model:value="limit" :options="[20,50,100].map(value => ({label:`${value} 条`,value}))" style="width: 110px"/>
      <n-text depth="3">勾选表格行可叠加比较日内资金曲线</n-text>
    </n-flex>
    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
    <n-grid :cols="2" :x-gap="12" responsive="screen">
      <n-gi>
        <n-data-table
          v-model:checked-row-keys="selectedCodes"
          :columns="columns"
          :data="rows"
          :loading="loading && !rows.length"
          :row-key="row => row._key"
          :max-height="560"
          :scroll-x="780"
          striped
        />
      </n-gi>
      <n-gi>
        <div ref="chartElement" class="flow-chart"/>
        <n-empty v-if="!loading && !selectedRows.some(row => historyFrom(row).length)" description="当前响应暂无资金时间线" class="flow-empty"/>
      </n-gi>
    </n-grid>
  </section>
</template>

<style scoped>
.flow-toolbar {
  gap: 10px;
  margin-bottom: 10px;
}

.flow-chart {
  width: 100%;
  height: 560px;
}

.flow-empty {
  margin-top: -330px;
  pointer-events: none;
}
</style>
