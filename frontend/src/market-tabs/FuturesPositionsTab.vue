<script setup>
import * as echarts from 'echarts'
import {computed, nextTick, onBeforeUnmount, onMounted, ref, toRef, watch} from 'vue'
import EvidenceStatusBar from '../components/EvidenceStatusBar.vue'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import {GetMarketFuturesPositions} from '../services/market-api.js'
import {normalizeFuturesPositionRows} from './market-data.js'

const props = defineProps({
  active: {type: Boolean, default: false},
  darkTheme: {type: Boolean, default: false},
  panelHeight: {type: Number, default: 620},
})

const active = toRef(props, 'active')
const symbol = ref('IF')
const selectedDate = ref(null)
const requestKey = computed(() => ['futures', symbol.value, selectedDate.value || ''].join('|'))
const chartElement = ref(null)
let chart = null
let resizeObserver = null

const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {rows: []},
  intervalMs: 300000,
  loader: () => GetMarketFuturesPositions({symbol: symbol.value, date: selectedDate.value}),
  requestKey,
})

const rows = computed(() => normalizeFuturesPositionRows(data.value))
const latest = computed(() => rows.value.at(-1))
const meta = computed(() => data.value && !Array.isArray(data.value) ? data.value : {})

const symbolOptions = [
  {label: 'IF 沪深300', value: 'IF'},
  {label: 'IH 上证50', value: 'IH'},
  {label: 'IC 中证500', value: 'IC'},
  {label: 'IM 中证1000', value: 'IM'},
]

const columns = [
  {title: '交易日', key: '_date', width: 110},
  {title: '结算价', key: '_settlePrice', width: 105},
  {title: '多单', key: '_long', width: 110, sorter: (a, b) => a._long - b._long},
  {title: '多单增减', key: '_longChange', width: 110, sorter: (a, b) => a._longChange - b._longChange},
  {title: '空单', key: '_short', width: 110, sorter: (a, b) => a._short - b._short},
  {title: '空单增减', key: '_shortChange', width: 110, sorter: (a, b) => a._shortChange - b._shortChange},
  {title: '净持仓', key: '_net', width: 110, sorter: (a, b) => a._net - b._net},
  {title: '指数收盘', key: '_indexClose', width: 110},
  {title: '指数涨跌', key: '_indexChange', width: 110},
  {title: '基差', key: '_basis', width: 100},
]

function renderChart() {
  if (!chart || chart.isDisposed()) return
  if (!rows.value.length) {
    chart.clear()
    return
  }
  const textColor = props.darkTheme ? '#c8c8c8' : '#4b5563'
  const dates = rows.value.map(row => row._date)
  chart.setOption({
    animation: false,
    tooltip: {trigger: 'axis', confine: true, axisPointer: {type: 'cross'}},
    legend: {type: 'scroll', top: 0, textStyle: {color: textColor}},
    axisPointer: {link: [{xAxisIndex: [0, 1]}]},
    grid: [
      {left: 74, right: 74, top: 48, height: '34%'},
      {left: 74, right: 74, top: '54%', height: '27%'},
    ],
    xAxis: [
      {type: 'category', data: dates, boundaryGap: false, axisLabel: {show: false}},
      {type: 'category', data: dates, boundaryGap: true, gridIndex: 1, axisLabel: {color: textColor, hideOverlap: true}},
    ],
    yAxis: [
      {type: 'value', scale: true, axisLabel: {color: textColor}, splitLine: {lineStyle: {type: 'dashed', opacity: 0.3}}},
      {type: 'value', scale: true, position: 'right', axisLabel: {color: textColor}, splitLine: {show: false}},
      {type: 'value', gridIndex: 1, axisLabel: {color: textColor}, splitLine: {lineStyle: {type: 'dashed', opacity: 0.3}}},
      {type: 'value', gridIndex: 1, position: 'right', axisLabel: {color: textColor}, splitLine: {show: false}},
    ],
    dataZoom: [{type: 'inside', xAxisIndex: [0, 1], start: 35, end: 100}, {type: 'slider', xAxisIndex: [0, 1], bottom: 4, height: 20}],
    series: [
      {name: '期货结算', type: 'line', xAxisIndex: 0, yAxisIndex: 0, showSymbol: false, data: rows.value.map(row => row._settlePrice), lineStyle: {color: '#f0a020'}},
      {name: '指数收盘', type: 'line', xAxisIndex: 0, yAxisIndex: 1, showSymbol: false, data: rows.value.map(row => row._indexClose), lineStyle: {color: '#2080f0'}},
      {name: '多单', type: 'bar', xAxisIndex: 1, yAxisIndex: 2, data: rows.value.map(row => row._long), itemStyle: {color: 'rgba(208,48,80,.65)'}},
      {name: '空单', type: 'bar', xAxisIndex: 1, yAxisIndex: 2, data: rows.value.map(row => row._short), itemStyle: {color: 'rgba(24,160,88,.65)'}},
      {name: '净持仓', type: 'line', xAxisIndex: 1, yAxisIndex: 3, showSymbol: false, data: rows.value.map(row => row._net), lineStyle: {color: '#8250df', width: 2}},
    ],
  }, true)
}

watch([rows, () => props.darkTheme], async () => { await nextTick(); renderChart() }, {deep: true})

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
  <section>
    <n-flex align="center" :wrap="true" class="futures-toolbar">
      <n-select v-model:value="symbol" :options="symbolOptions" style="width: 160px"/>
      <n-date-picker v-model:formatted-value="selectedDate" type="date" value-format="yyyy-MM-dd" clearable :is-date-disabled="ts => ts > Date.now()" style="width: 150px"/>
      <n-tag type="info" :bordered="false">{{ meta.varietyName || meta.variety || symbol }}</n-tag>
      <n-tag v-if="meta.contractCode" :bordered="false">主力合约 {{ meta.contractCode }}</n-tag>
      <n-tag v-if="meta.indexCode" :bordered="false">对应指数 {{ meta.indexCode }}</n-tag>
      <n-text depth="3">日度持仓时间序列，通常在交易日收盘后更新</n-text>
    </n-flex>
    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
    <n-grid v-if="latest" :cols="8" :x-gap="12" class="futures-summary">
      <n-gi><n-statistic label="最新交易日" :value="latest._date"/></n-gi>
      <n-gi><n-statistic label="结算价" :value="latest._settlePrice"/></n-gi>
      <n-gi><n-statistic label="多单持仓" :value="latest._long"/></n-gi>
      <n-gi><n-statistic label="空单持仓" :value="latest._short"/></n-gi>
      <n-gi><n-statistic label="净持仓" :value="latest._net"/></n-gi>
      <n-gi><n-statistic label="指数收盘" :value="latest._indexClose"/></n-gi>
      <n-gi><n-statistic label="指数涨跌" :value="latest._indexChange"/></n-gi>
      <n-gi><n-statistic label="基差" :value="latest._basis"/></n-gi>
    </n-grid>
    <n-spin :show="loading && !rows.length">
      <div ref="chartElement" class="futures-chart" :style="{height: Math.max(420, panelHeight - 190) + 'px'}"/>
      <n-empty v-if="!loading && !rows.length" description="暂无期指持仓数据" class="chart-empty"/>
    </n-spin>
    <n-data-table v-if="rows.length" :columns="columns" :data="rows" :row-key="row => `${meta.contractCode || symbol}-${row._date}`" :scroll-x="1080" :max-height="360" striped/>
  </section>
</template>

<style scoped>
.futures-toolbar,
.futures-summary {
  margin-bottom: 10px;
}

.futures-chart {
  width: 100%;
}

.chart-empty {
  margin-top: -300px;
  pointer-events: none;
}
</style>
