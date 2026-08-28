<script setup>
import {computed, ref, watch} from 'vue'
import {useMessage} from 'naive-ui'
import {GetAIRecommendationChart, RefreshAIRecommendationChart} from '../services/research-api'
import {formatMoney, formatPercent, formatPrice} from '../utils/number-format'
import {adaptResearchChart, researchChartOverlays} from '../charting/research-chart-adapter.js'
import {useResearchChartPreferences} from '../composables/useResearchChartPreferences'
import ChartDataMeta from './chart/ChartDataMeta.vue'
import MarketChartCanvas from './chart/MarketChartCanvas.vue'

const props = defineProps({
  recommendationId: {type: String, required: true},
  fallbackTrades: {type: Array, default: () => []},
})

const message = useMessage()
const {showPriceLines} = useResearchChartPreferences()
const chartCanvas = ref(null)
const chartData = ref(null)
const initialLoading = ref(false)
const refreshing = ref(false)
const mode = ref('line')
const selectedSession = ref(null)
const cacheError = ref('')
const refreshError = ref('')
let requestVersion = 0

const model = computed(() => adaptResearchChart(chartData.value || {}))
const trades = computed(() => chartData.value?.trades?.length ? chartData.value.trades : props.fallbackTrades)
const sessions = computed(() => chartData.value?.sessions || [])
const sessionOptions = computed(() => sessions.value.map(item => ({label: `${item.date}${item.status === 'missing' ? '（缺失）' : ''}`, value: item.date})))
const hasBuyTrade = computed(() => trades.value.some(item => String(item.side).toLowerCase() === 'buy'))
const isPartial = computed(() => chartData.value?.status === 'partial')
const isEmpty = computed(() => chartData.value?.status === 'empty' || (!initialLoading.value && model.value.bars.length === 0))
const currentYield = computed(() => Number(chartData.value?.currentNetYieldRate || 0))
const overlays = computed(() => researchChartOverlays(model.value, trades.value, {showPriceLines: showPriceLines.value}))
const chartConfig = computed(() => ({
  viewMode: mode.value,
  mainIndicators: [],
  subIndicator: 'VOL',
  initialVisibleBars: 300,
}))

function resetRange() {
  selectedSession.value = null
  chartCanvas.value?.resetZoom()
}

function locateSession(date) {
  if (!date) return
  const found = chartCanvas.value?.zoomToTimeRange(`${date}T00:00:00`, `${date}T23:59:59`)
  if (!found) message.warning(`${date} 暂无分钟数据`)
}

async function refreshChart(automatic = false, version = requestVersion) {
  if (!props.recommendationId || refreshing.value || version !== requestVersion) return
  refreshing.value = true
  refreshError.value = ''
  try {
    const result = await RefreshAIRecommendationChart(props.recommendationId)
    if (version !== requestVersion) return
    chartData.value = result
  } catch (reason) {
    if (version !== requestVersion) return
    refreshError.value = reason?.message || String(reason)
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
  } catch (reason) {
    if (version !== requestVersion) return
    cacheError.value = reason?.message || String(reason)
  } finally {
    if (version === requestVersion) initialLoading.value = false
  }
  if (version === requestVersion) await refreshChart(true, version)
}

watch(() => props.recommendationId, () => { void loadInitial() }, {immediate: true})
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
        <n-flex align="center" :size="6" class="price-lines-toggle">
          <n-text depth="3">价格横线</n-text>
          <n-switch v-model:value="showPriceLines" aria-label="价格横线">
            <template #checked>显示</template>
            <template #unchecked>隐藏</template>
          </n-switch>
        </n-flex>
      </n-flex>
      <n-flex align="center" :wrap="true">
        <n-statistic label="最新价" :value="chartData?.currentPrice ? formatPrice(chartData.currentPrice) : '--'"/>
        <n-statistic label="预估净收益" :value="chartData && hasBuyTrade ? formatMoney(chartData.currentNetPnl) : '--'"/>
        <n-text :type="currentYield >= 0 ? 'error' : 'success'" strong>{{ chartData && hasBuyTrade ? formatPercent(currentYield) : '--' }}</n-text>
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

    <ChartDataMeta :model="model" :loading="initialLoading || refreshing" :error="refreshError || cacheError" @refresh="refreshChart(false)"/>
    <n-spin :show="initialLoading || (refreshing && !chartData)" description="正在读取分钟行情">
      <MarketChartCanvas
          v-show="model.bars.length"
          ref="chartCanvas"
          :model="model"
          :config="chartConfig"
          :overlays="overlays"
          :height="520"
      />
      <n-empty v-if="!model.bars.length && !initialLoading && !refreshing" description="暂无分钟走势" class="chart-empty"/>
    </n-spin>
    <n-flex justify="space-between" class="chart-footnote">
      <n-text depth="3">十字光标保留 OHLC、量价来源及扣除全部交易成本后的逐分钟净收益；真实成交标记不会被普通证券行情替换。</n-text>
      <n-text depth="3">数据截至 {{ String(chartData?.refreshedAt || chartData?.quoteAt || '--').replace('T', ' ').slice(0, 19) }}</n-text>
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

.chart-alert {
  margin: 8px 0;
}

.chart-empty {
  height: 320px;
  justify-content: center;
}

.chart-footnote {
  gap: 12px;
  margin-top: 4px;
}
</style>
