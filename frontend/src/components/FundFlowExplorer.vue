<script setup>
import * as echarts from 'echarts'
import {NText} from 'naive-ui'
import {computed, h, nextTick, onBeforeUnmount, onMounted, ref, toRef, watch} from 'vue'
import {GetMarketFundFlows, GetMarketFundFlowTimeline} from '../services/market-api.js'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import EvidenceStatusBar from './EvidenceStatusBar.vue'
import {
  compareOptional,
  formatFlowAmount,
  formatFlowPercent,
  fundFlowSortOptions,
  fundFlowTone,
  fundFlowTradingDate,
  limitedFundFlowSelection,
  normalizeFundFlowRows,
} from './fund-flow-model.js'

const props = defineProps({
  active: {type: Boolean, default: false},
  darkTheme: {type: Boolean, default: false},
  scope: {type: String, required: true, validator: value => ['sector', 'concept'].includes(value)},
})

const active = toRef(props, 'active')
const sort = ref('netamount')
const limit = ref(100)
const requestKey = computed(() => ['fund-flow', props.scope, sort.value, limit.value].join('|'))
const selectedCodes = ref([])
const chartElement = ref(null)
const timelineEnvelopes = ref([])
const timelineLoading = ref(false)
const timelineError = ref('')
let chart = null
let resizeObserver = null
let timelineVersion = 0
let selectionInitialized = false

const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {rows: []},
  intervalMs: 60000,
  loader: () => GetMarketFundFlows({scope: props.scope, sort: sort.value, limit: limit.value}),
  requestKey,
})

const rows = computed(() => normalizeFundFlowRows(data.value))

function amountColumn(title, key, width = 138) {
  return {
    title, key, width, sorter: (left, right) => compareOptional(left[key], right[key]),
    render: row => h(NText, {type: fundFlowTone(row[key])}, {default: () => formatFlowAmount(row[key])}),
  }
}

const columns = computed(() => [
  {type: 'selection', multiple: true, width: 42},
  {title: '代码', key: '_key', width: 110},
  {title: props.scope === 'sector' ? '板块' : '概念', key: '_name', minWidth: 150, ellipsis: {tooltip: true}},
  amountColumn('主力净流入', '_netInflow'),
  {
    title: '主力净占比', key: '_mainNetRatio', width: 125, sorter: (left, right) => compareOptional(left._mainNetRatio, right._mainNetRatio),
    render: row => h(NText, {type: fundFlowTone(row._mainNetRatio)}, {default: () => formatFlowPercent(row._mainNetRatio)}),
  },
  amountColumn('超大单净流入', '_superLargeNetAmount'),
  amountColumn('大单净流入', '_largeNetAmount'),
  amountColumn('中单净流入', '_mediumNetAmount'),
  amountColumn('小单净流入', '_smallNetAmount'),
  {
    title: '涨跌幅', key: '_changePercent', width: 110, sorter: (left, right) => compareOptional(left._changePercent, right._changePercent),
    render: row => h(NText, {type: fundFlowTone(row._changePercent)}, {default: () => formatFlowPercent(row._changePercent)}),
  },
])

const selectedRows = computed(() => {
  const selected = new Set(selectedCodes.value)
  return rows.value.filter(row => selected.has(row._key))
})
const actualTradingDate = computed(() => fundFlowTradingDate(envelope.value, timelineEnvelopes.value))
const hasTimeline = computed(() => timelineEnvelopes.value.some(item => item.data?.points?.length))

function updateSelectedCodes(keys) {
  selectionInitialized = true
  selectedCodes.value = limitedFundFlowSelection(keys)
}

function renderChart() {
  if (!chart || chart.isDisposed()) return
  const timelines = timelineEnvelopes.value.filter(item => Array.isArray(item.data?.points) && item.data.points.length)
  const allTimes = [...new Set(timelines.flatMap(item => item.data.points.map(point => String(point.at || ''))).filter(Boolean))].sort()
  if (!allTimes.length) {
    chart.clear()
    return
  }
  const textColor = props.darkTheme ? '#c8c8c8' : '#4b5563'
  const series = timelines.map(item => {
    const values = new Map(item.data.points.map(point => [String(point.at || ''), Number(point.mainNetAmount)]))
    return {
      name: item.data.name || item.data.code,
      type: 'line',
      showSymbol: false,
      smooth: false,
      tooltip: {valueFormatter: formatFlowAmount},
      data: allTimes.map(time => values.get(time) ?? null),
    }
  })
  chart.setOption({
    animation: false,
    tooltip: {trigger: 'axis', confine: true, axisPointer: {type: 'cross'}},
    legend: {type: 'scroll', top: 0, textStyle: {color: textColor}},
    grid: {left: 12, right: 24, top: 54, bottom: 54, containLabel: true},
    xAxis: {type: 'category', boundaryGap: false, data: allTimes, axisLabel: {color: textColor, hideOverlap: true, formatter: value => String(value).slice(11, 16)}},
    yAxis: {type: 'value', scale: true, name: '主力净流入', axisLabel: {color: textColor, formatter: formatFlowAmount}, splitLine: {lineStyle: {type: 'dashed', opacity: 0.35}}},
    dataZoom: [{type: 'inside', start: 0, end: 100}, {type: 'slider', height: 20, bottom: 8}],
    series,
  }, true)
}

async function refreshTimelines() {
  const version = ++timelineVersion
  const codes = selectedRows.value.map(row => row._key).filter(code => /^BK\d{4}$/i.test(code)).slice(0, 6)
  timelineEnvelopes.value = []
  timelineError.value = ''
  if (!codes.length) {
    timelineLoading.value = false
    renderChart()
    return
  }
  timelineLoading.value = true
  const results = await Promise.allSettled(codes.map(code => GetMarketFundFlowTimeline(code)))
  if (version !== timelineVersion) return
  const successful = []
  const failures = []
  for (const result of results) {
    if (result.status === 'fulfilled' && ['ok', 'partial', 'stale'].includes(result.value?.status) && result.value?.data?.points?.length) {
      successful.push(result.value)
    } else {
      const reason = result.status === 'rejected' ? result.reason : result.value?.errors?.[0]
      failures.push(reason?.message || String(reason || '资金时间线不可用'))
    }
  }
  timelineEnvelopes.value = successful
  timelineError.value = [...new Set(failures)].join('；')
  timelineLoading.value = false
  await nextTick()
  renderChart()
}

async function refreshAll() {
  timelineVersion += 1
  timelineEnvelopes.value = []
  timelineError.value = ''
  timelineLoading.value = true
  renderChart()
  if (!await refresh()) {
    timelineLoading.value = false
    timelineError.value = error.value || '排行刷新失败，请重试'
  }
}

watch(rows, nextRows => {
  if (!nextRows.length) selectionInitialized = false
  const available = new Set(nextRows.map(row => row._key))
  const retained = selectedCodes.value.filter(code => available.has(code))
  if (selectionInitialized) selectedCodes.value = retained
  else {
    selectedCodes.value = [...nextRows]
      .filter(row => row._netInflow !== null)
      .sort((left, right) => right._netInflow - left._netInflow)
      .slice(0, 3)
      .map(row => row._key)
  }
  if (nextRows.length) selectionInitialized = true
}, {deep: true})
watch(selectedRows, () => { void refreshTimelines() }, {deep: true})
watch([timelineEnvelopes, () => props.darkTheme], async () => { await nextTick(); renderChart() }, {deep: true})
onMounted(() => {
  if (!chartElement.value) return
  chart = echarts.init(chartElement.value)
  resizeObserver = new ResizeObserver(() => chart?.resize())
  resizeObserver.observe(chartElement.value)
  renderChart()
})

onBeforeUnmount(() => {
  timelineVersion += 1
  resizeObserver?.disconnect()
  if (chart && !chart.isDisposed()) chart.dispose()
  chart = null
})
</script>

<template>
  <section class="fund-flow-explorer">
    <n-flex align="center" :wrap="true" class="flow-toolbar">
      <n-tag :bordered="false" type="info">最新交易日 {{ actualTradingDate }}</n-tag>
      <n-select v-model:value="sort" :options="fundFlowSortOptions" style="width: 160px"/>
      <n-select v-model:value="limit" :options="[20,50,100].map(value => ({label:`${value} 条`,value}))" style="width: 110px"/>
      <n-text depth="3">勾选表格行可叠加比较日内主力净流入，最多显示 6 条</n-text>
    </n-flex>
    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refreshAll"/>
    <n-grid cols="1 l:2" :x-gap="12" :y-gap="12" responsive="screen">
      <n-gi>
        <n-data-table
          :checked-row-keys="selectedCodes"
          @update:checked-row-keys="updateSelectedCodes"
          :columns="columns"
          :data="rows"
          :loading="loading && !rows.length"
          :row-key="row => row._key"
          :max-height="560"
          :scroll-x="1320"
          striped
        />
      </n-gi>
      <n-gi>
        <n-spin :show="timelineLoading" description="正在加载资金时间线">
          <div ref="chartElement" class="flow-chart"/>
          <n-empty
            v-if="!timelineLoading && !hasTimeline"
            :description="selectedCodes.length ? (timelineError || '所选项目暂无资金时间线') : '请勾选左侧项目查看资金时间线'"
            class="flow-empty"
          />
        </n-spin>
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
