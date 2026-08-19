<script setup>
import * as echarts from 'echarts'
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {useMessage} from 'naive-ui'
import {GetAIRecommendationChart, RefreshAIRecommendationChart} from '../services/research-api'

const props = defineProps({
  recommendationId: {type: String, required: true},
  fallbackTrades: {type: Array, default: () => []},
})

const message = useMessage()
const chartElement = ref(null)
const chartData = ref(null)
const initialLoading = ref(false)
const refreshing = ref(false)
const mode = ref('line')
const selectedSession = ref(null)
const cacheError = ref('')
const refreshError = ref('')
let chart = null
let resizeObserver = null
let requestVersion = 0

const bars = computed(() => [...(chartData.value?.bars || [])].sort((left, right) => new Date(left.at) - new Date(right.at)))
const trades = computed(() => chartData.value?.trades?.length ? chartData.value.trades : props.fallbackTrades)
const sessions = computed(() => chartData.value?.sessions || [])
const sessionOptions = computed(() => sessions.value.map(item => ({label: `${item.date}${item.status === 'missing' ? '（缺失）' : ''}`, value: item.date})))
const hasBuyTrade = computed(() => trades.value.some(item => String(item.side).toLowerCase() === 'buy'))
const isPartial = computed(() => chartData.value?.status === 'partial')
const isEmpty = computed(() => chartData.value?.status === 'empty' || (!initialLoading.value && bars.value.length === 0))
const currentYield = computed(() => Number(chartData.value?.currentNetYieldRate || 0))

function finite(value, fallback = 0) {
  const number = Number(value)
  return Number.isFinite(number) ? number : fallback
}

function money(value) {
  const number = finite(value)
  return `${number < 0 ? '-' : ''}¥${Math.abs(number).toFixed(2)}`
}

function percent(value) {
  const number = finite(value) * 100
  return `${number >= 0 ? '+' : ''}${number.toFixed(2)}%`
}

function compactNumber(value) {
  const number = finite(value)
  if (Math.abs(number) >= 100000000) return `${(number / 100000000).toFixed(2)}亿`
  if (Math.abs(number) >= 10000) return `${(number / 10000).toFixed(2)}万`
  return number.toFixed(0)
}

function dateTime(value) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return String(value).replace('T', ' ').slice(0, 19)
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
    second: '2-digit', hour12: false,
  }).format(date).replaceAll('/', '-')
}

function shortTime(value) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return String(value).slice(0, 16).replace('T', ' ')
  const parts = new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(date)
  return parts.replaceAll('/', '-')
}

function sessionKey(value) {
  return String(value || '').slice(0, 10)
}

function escapeHTML(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;')
}

function previousCloseFor(bar) {
  return finite(sessions.value.find(item => item.date === sessionKey(bar.at))?.previousClose)
}

function tooltipHTML(params) {
  const first = Array.isArray(params) ? params.find(item => item.seriesName === (mode.value === 'line' ? '分时' : '1分钟K')) : null
  const bar = bars.value[first?.dataIndex]
  if (!bar) return ''
  const previousClose = previousCloseFor(bar)
  const change = previousClose > 0 ? (finite(bar.close) - previousClose) / previousClose : 0
  const barTime = new Date(bar.at).getTime()
  const profitAvailable = bar.netPnl !== null && bar.netPnl !== undefined && trades.value.some(item => {
    return String(item.side).toLowerCase() === 'buy' && new Date(item.tradedAt).getTime() <= barTime + 60000
  })
  return [
    `<strong>${escapeHTML(dateTime(bar.at))}</strong>`,
    `开盘：${finite(bar.open).toFixed(3)}　最高：${finite(bar.high).toFixed(3)}`,
    `最低：${finite(bar.low).toFixed(3)}　收盘：${finite(bar.close).toFixed(3)}`,
    `涨跌幅：<span style="color:${change >= 0 ? '#d03050' : '#18a058'}">${percent(change)}</span>`,
    `成交量：${escapeHTML(compactNumber(bar.volume))}　成交额：${escapeHTML(compactNumber(bar.amount))}`,
    profitAvailable ? `预估净收益：<span style="color:${finite(bar.netPnl) >= 0 ? '#d03050' : '#18a058'}">${escapeHTML(money(bar.netPnl))}（${escapeHTML(percent(bar.netYieldRate))}）</span>` : '预估净收益：--',
    `来源：${escapeHTML(bar.source || '--')}`,
  ].join('<br>')
}

function tradeMarkPoints(categories) {
  const categorySet = new Set(categories)
  return trades.value.flatMap(item => {
    if (!item.markerAt || !categorySet.has(item.markerAt)) return []
    const isBuy = String(item.side).toLowerCase() === 'buy'
    return [{
      name: isBuy ? '买入' : '卖出',
      coord: [item.markerAt, finite(item.executionPrice)],
      value: isBuy ? 'B' : 'S',
      symbol: 'pin',
      symbolSize: 48,
      itemStyle: {color: isBuy ? '#d03050' : '#18a058'},
      label: {show: true, color: '#fff', fontWeight: 'bold', formatter: isBuy ? 'B' : 'S'},
      tooltip: {
        formatter: `${isBuy ? '买入' : '卖出'} ${escapeHTML(dateTime(item.tradedAt))}<br>成交价：${finite(item.executionPrice).toFixed(3)}<br>数量：${finite(item.quantity).toFixed(0)}<br>费用：${money(item.totalFees)}${item.markerSnapped ? '<br>标记已吸附至最近分钟柱' : ''}`,
      },
    }]
  })
}

function buildMarkPoints(categories) {
  if (!bars.value.length) return []
  const highest = bars.value.reduce((result, item) => finite(item.high) > finite(result.high) ? item : result)
  const lowest = bars.value.reduce((result, item) => finite(item.low) < finite(result.low) ? item : result)
  const latest = bars.value.at(-1)
  return [
    ...tradeMarkPoints(categories),
    {name: '区间最高', coord: [highest.at, finite(highest.high)], value: finite(highest.high).toFixed(3), symbol: 'circle', symbolSize: 9, label: {show: true, position: 'top', color: '#d03050', formatter: '高 {c}'}},
    {name: '区间最低', coord: [lowest.at, finite(lowest.low)], value: finite(lowest.low).toFixed(3), symbol: 'circle', symbolSize: 9, label: {show: true, position: 'bottom', color: '#18a058', formatter: '低 {c}'}},
    {name: '最新', coord: [latest.at, finite(latest.close)], value: finite(latest.close).toFixed(3), symbol: 'circle', symbolSize: 10, label: {show: true, position: 'right', color: '#2080f0', formatter: '最新 {c}'}},
  ]
}

function weightedBuyPrice() {
  const buys = trades.value.filter(item => String(item.side).toLowerCase() === 'buy' && finite(item.quantity) > 0)
  const quantity = buys.reduce((sum, item) => sum + finite(item.quantity), 0)
  return quantity > 0 ? buys.reduce((sum, item) => sum + finite(item.executionPrice) * finite(item.quantity), 0) / quantity : 0
}

function renderChart() {
  if (!chart || chart.isDisposed()) return
  if (!bars.value.length) {
    chart.clear()
    return
  }
  const categories = bars.value.map(item => item.at)
  const visibleStart = Math.max(0, 100 - (Math.min(300, bars.value.length) / bars.value.length) * 100)
  const buyPrice = weightedBuyPrice()
  const latestPrice = finite(chartData.value?.currentPrice, finite(bars.value.at(-1)?.close))
  const mainData = mode.value === 'line'
    ? bars.value.map(item => finite(item.close))
    : bars.value.map(item => [finite(item.open), finite(item.close), finite(item.low), finite(item.high)])
  const markLines = []
  if (buyPrice > 0) markLines.push({name: '买入均价', yAxis: buyPrice, label: {formatter: `买入 ${buyPrice.toFixed(3)}`}, lineStyle: {color: '#d03050', type: 'dashed'}})
  if (latestPrice > 0) markLines.push({name: '最新价', yAxis: latestPrice, label: {formatter: `最新 ${latestPrice.toFixed(3)}`}, lineStyle: {color: '#2080f0', type: 'dotted'}})
  const mainSeries = {
    name: mode.value === 'line' ? '分时' : '1分钟K',
    type: mode.value === 'line' ? 'line' : 'candlestick',
    data: mainData,
    showSymbol: false,
    sampling: mode.value === 'line' ? 'lttb' : undefined,
    lineStyle: mode.value === 'line' ? {color: '#2080f0', width: 1.5} : undefined,
    areaStyle: mode.value === 'line' ? {color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{offset: 0, color: 'rgba(32,128,240,.22)'}, {offset: 1, color: 'rgba(32,128,240,.02)'}])} : undefined,
    itemStyle: mode.value === 'candle' ? {color: '#d03050', color0: '#18a058', borderColor: '#d03050', borderColor0: '#18a058'} : undefined,
    markPoint: {symbolKeepAspect: true, data: buildMarkPoints(categories)},
    markLine: {symbol: ['none', 'none'], silent: true, data: markLines},
  }
  chart.setOption({
    animation: false,
    grid: [
      {left: 72, right: 78, top: 26, height: 320},
      {left: 72, right: 78, top: 382, height: 72},
    ],
    tooltip: {
      trigger: 'axis',
      confine: true,
      axisPointer: {type: 'cross'},
      formatter: tooltipHTML,
      extraCssText: 'line-height:1.75;box-shadow:0 4px 18px rgba(0,0,0,.18);',
    },
    axisPointer: {link: [{xAxisIndex: [0, 1]}], label: {backgroundColor: '#5c677d'}},
    xAxis: [
      {type: 'category', data: categories, boundaryGap: true, axisLabel: {formatter: shortTime, hideOverlap: true}, axisLine: {show: true}, axisTick: {show: true}},
      {type: 'category', gridIndex: 1, data: categories, boundaryGap: true, axisLabel: {show: false}, axisTick: {show: false}},
    ],
    yAxis: [
      {type: 'value', scale: true, position: 'right', axisLabel: {formatter: value => finite(value).toFixed(2)}, axisLine: {show: true}, axisTick: {show: true}, splitLine: {lineStyle: {type: 'dashed', opacity: 0.35}}},
      {type: 'value', scale: true, gridIndex: 1, position: 'right', splitNumber: 2, axisLabel: {formatter: compactNumber}, axisLine: {show: true}, axisTick: {show: true}, splitLine: {show: false}},
    ],
    dataZoom: [
      {type: 'inside', xAxisIndex: [0, 1], start: visibleStart, end: 100, zoomOnMouseWheel: true, moveOnMouseMove: true},
      {type: 'slider', xAxisIndex: [0, 1], start: visibleStart, end: 100, height: 22, bottom: 10, brushSelect: true},
    ],
    series: [
      mainSeries,
      {
        name: '成交量', type: 'bar', xAxisIndex: 1, yAxisIndex: 1,
        data: bars.value.map(item => ({value: finite(item.volume), itemStyle: {color: finite(item.close) >= finite(item.open) ? 'rgba(208,48,80,.7)' : 'rgba(24,160,88,.7)'}})),
      },
    ],
  }, true)
}

function resetRange() {
  if (!chart) return
  selectedSession.value = null
  chart.dispatchAction({type: 'dataZoom', dataZoomIndex: 0, start: 0, end: 100})
  chart.dispatchAction({type: 'dataZoom', dataZoomIndex: 1, start: 0, end: 100})
}

function locateSession(date) {
  if (!chart || !date) return
  const indices = bars.value.map((item, index) => sessionKey(item.at) === date ? index : -1).filter(index => index >= 0)
  if (!indices.length) {
    message.warning(`${date} 暂无分钟数据`)
    return
  }
  chart.dispatchAction({type: 'dataZoom', dataZoomIndex: 0, startValue: indices[0], endValue: indices.at(-1)})
  chart.dispatchAction({type: 'dataZoom', dataZoomIndex: 1, startValue: indices[0], endValue: indices.at(-1)})
}

async function refreshChart(automatic = false, version = requestVersion) {
  if (!props.recommendationId || refreshing.value || version !== requestVersion) return
  refreshing.value = true
  refreshError.value = ''
  try {
    const result = await RefreshAIRecommendationChart(props.recommendationId)
    if (version !== requestVersion) return
    chartData.value = result
  } catch (error) {
    if (version !== requestVersion) return
    refreshError.value = error?.message || String(error)
    if (!automatic) message.error(refreshError.value)
  } finally {
    if (version === requestVersion) refreshing.value = false
  }
}

async function loadInitial() {
  const version = ++requestVersion
  chartData.value = null
  cacheError.value = ''
  refreshError.value = ''
  selectedSession.value = null
  initialLoading.value = true
  try {
    chartData.value = await GetAIRecommendationChart(props.recommendationId)
  } catch (error) {
    if (version !== requestVersion) return
    cacheError.value = error?.message || String(error)
  } finally {
    if (version === requestVersion) initialLoading.value = false
  }
  if (version === requestVersion) await refreshChart(true, version)
}

watch(() => props.recommendationId, () => { void loadInitial() }, {immediate: true})
watch([bars, mode], async () => { await nextTick(); renderChart() }, {deep: true})

onMounted(() => {
  if (chartElement.value) {
    chart = echarts.init(chartElement.value)
    resizeObserver = new ResizeObserver(() => chart?.resize())
    resizeObserver.observe(chartElement.value)
    renderChart()
  }
})

onBeforeUnmount(() => {
  requestVersion++
  resizeObserver?.disconnect()
  if (chart && !chart.isDisposed()) chart.dispose()
  chart = null
})
</script>

<template>
  <section class="research-trade-chart">
    <n-flex justify="space-between" align="center" :wrap="true" class="chart-toolbar">
      <n-flex align="center" :wrap="true">
        <n-button-group>
          <n-button :type="mode === 'line' ? 'primary' : 'default'" @click="mode = 'line'">分时</n-button>
          <n-button :type="mode === 'candle' ? 'primary' : 'default'" @click="mode = 'candle'">1分钟K</n-button>
        </n-button-group>
        <n-select v-model:value="selectedSession" clearable placeholder="定位交易日" :options="sessionOptions" style="width:170px" @update:value="locateSession"/>
        <n-button @click="resetRange">范围复位</n-button>
      </n-flex>
      <n-flex align="center" :wrap="true">
        <n-statistic label="最新价" :value="chartData?.currentPrice ? Number(chartData.currentPrice).toFixed(3) : '--'"/>
        <n-statistic label="预估净收益" :value="chartData && hasBuyTrade ? money(chartData.currentNetPnl) : '--'"/>
        <n-text :type="currentYield >= 0 ? 'error' : 'success'" strong>{{ chartData && hasBuyTrade ? percent(currentYield) : '--' }}</n-text>
        <n-button type="primary" :loading="refreshing" @click="refreshChart(false)">刷新行情</n-button>
      </n-flex>
    </n-flex>

    <n-alert v-if="isPartial" type="warning" :bordered="false" class="chart-alert">
      分钟数据不完整，图中仅展示已取得的真实数据；缺失交易日：{{ chartData.missingSessions?.join('、') || '部分时段' }}。
    </n-alert>
    <n-alert v-else-if="isEmpty && !initialLoading && !refreshing" type="info" :bordered="false" class="chart-alert">
      暂无可展示的分钟数据{{ chartData?.missingSessions?.length ? `；缺失交易日：${chartData.missingSessions.join('、')}` : '' }}。
    </n-alert>
    <n-alert v-if="refreshError || (cacheError && !chartData)" type="error" :bordered="false" class="chart-alert">
      {{ refreshError || cacheError }}。{{ chartData ? '已保留上次缓存图表。' : '' }}
    </n-alert>
    <n-collapse v-if="chartData?.providerErrors?.length" class="provider-errors">
      <n-collapse-item title="数据源异常详情" name="errors">
        <n-list size="small">
          <n-list-item v-for="item in chartData.providerErrors" :key="`${item.provider}-${item.message}`">
            <n-text strong>{{ item.provider }}</n-text>：{{ item.message }}
          </n-list-item>
        </n-list>
      </n-collapse-item>
    </n-collapse>

    <n-spin :show="initialLoading || (refreshing && !chartData)" description="正在读取分钟行情">
      <div v-show="bars.length" ref="chartElement" class="chart-canvas"/>
      <n-empty v-if="!bars.length && !initialLoading && !refreshing" description="暂无分钟走势" class="chart-empty"/>
    </n-spin>
    <n-flex justify="space-between" class="chart-footnote">
      <n-text depth="3">十字光标可查看 OHLC、量价来源及扣除全部交易成本后的逐分钟净收益；滚轮缩放，按住拖动平移。</n-text>
      <n-text depth="3">数据截至 {{ dateTime(chartData?.refreshedAt || chartData?.quoteAt) }}</n-text>
    </n-flex>
  </section>
</template>

<style scoped>
.research-trade-chart {
  width: 100%;
}

.chart-toolbar {
  gap: 16px;
  margin-bottom: 10px;
}

.chart-toolbar :deep(.n-statistic) {
  min-width: 104px;
}

.chart-toolbar :deep(.n-statistic-value__content) {
  font-size: 18px;
}

.chart-alert,
.provider-errors {
  margin: 8px 0;
}

.chart-canvas {
  width: 100%;
  height: 520px;
  min-height: 520px;
}

.chart-empty {
  height: 320px;
  justify-content: center;
}

.chart-footnote {
  gap: 12px;
  margin-top: 4px;
}

@media (max-width: 900px) {
  .chart-canvas {
    height: 480px;
    min-height: 480px;
  }
}
</style>
