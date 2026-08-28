<script setup>
import {computed, ref, watch} from 'vue'
import {useMessage} from 'naive-ui'
import {CHART_ADJUSTMENTS, CHART_PERIODS, chartModelFromEnvelope, defaultChartAdjustment, isLegacyChartInstrument, normalizeAdjustment, normalizeInstrument} from '../../charting/chart-contract.js'
import {activeDrawings, DRAWING_TOOLS, drawingPointCount} from '../../charting/drawing-model.js'
import {useChartDrawings} from '../../composables/useChartDrawings.js'
import {useInstrumentChart} from '../../composables/useInstrumentChart.js'
import InstrumentMicrostructureDrawer from '../InstrumentMicrostructureDrawer.vue'
import ChartDataMeta from './ChartDataMeta.vue'
import MarketChartCanvas from './MarketChartCanvas.vue'

const props = defineProps({
  code: {type: String, default: ''},
  name: {type: String, default: ''},
  assetType: {type: String, default: 'stock'},
  market: {type: String, default: ''},
  initialPeriod: {type: String, default: 'day'},
  initialAdjustment: {type: String, default: ''},
  initialViewMode: {type: String, default: 'candle'},
  chartHeight: {type: Number, default: 520},
  initialVisibleBars: {type: Number, default: 120},
  darkTheme: {type: Boolean, default: false},
  compact: {type: Boolean, default: false},
  showMicrostructure: {type: Boolean, default: true},
})

const message = useMessage()
const chartCanvas = ref(null)
const period = ref(props.initialPeriod)
const adjustment = ref(normalizeAdjustment(props.initialAdjustment || defaultChartAdjustment(props.assetType), props.assetType))
const viewMode = ref(props.initialViewMode)
const mainIndicators = ref(['MA'])
const subIndicator = ref('VOL')
const drawingTool = ref('')
const pendingPoints = ref([])
const microstructureVisible = ref(false)

const instrument = computed(() => normalizeInstrument({assetType: props.assetType, market: props.market, code: props.code}))
const legacy = computed(() => isLegacyChartInstrument(instrument.value))
const adjustmentOptions = computed(() => CHART_ADJUSTMENTS.map(item => ({...item, disabled: (props.assetType === 'index' || legacy.value) && item.value !== 'none'})))
const periodOptions = computed(() => CHART_PERIODS.map(item => ({...item, disabled: legacy.value && item.value !== 'day'})))
const query = computed(() => ({
  code: instrument.value.code,
  assetType: instrument.value.assetType,
  market: instrument.value.market,
  period: period.value,
  adjustment: adjustment.value,
  name: props.name,
  legacy: legacy.value,
  limit: period.value.endsWith('m') ? 5000 : 1200,
}))
const {envelope, loading, error, refresh} = useInstrumentChart(query)
const model = computed(() => {
  const normalized = chartModelFromEnvelope(envelope.value, instrument.value)
  return {...normalized, name: normalized.name || props.name, period: period.value, adjustment: adjustment.value}
})
const drawingScope = computed(() => ({
  instrument: legacy.value ? {...instrument.value, code: ''} : (model.value.instrument || instrument.value),
  period: period.value,
  adjustment: adjustment.value,
}))
const drawingStore = useChartDrawings(drawingScope)
const visibleDrawings = computed(() => activeDrawings(drawingStore.document.value.drawings))
const chartConfig = computed(() => ({
  viewMode: viewMode.value,
  mainIndicators: mainIndicators.value,
  subIndicator: subIndicator.value,
  initialVisibleBars: props.initialVisibleBars,
  darkTheme: props.darkTheme,
}))
const drawingHint = computed(() => {
  if (!drawingTool.value) return ''
  const target = drawingPointCount(drawingTool.value)
  return `请在主图选择锚点 ${pendingPoints.value.length + 1}/${target}`
})

watch(() => props.assetType, assetType => {
  adjustment.value = normalizeAdjustment(props.initialAdjustment || defaultChartAdjustment(assetType), assetType)
})
watch(legacy, value => {
  if (!value) return
  period.value = 'day'
  adjustment.value = 'none'
}, {immediate: true})
watch(() => props.initialPeriod, value => { if (value) period.value = value })
watch(() => props.initialAdjustment, value => { if (value) adjustment.value = normalizeAdjustment(value, props.assetType) })
watch(() => props.initialViewMode, value => { if (value) viewMode.value = value })
watch([period, adjustment, () => props.code], () => {
  drawingTool.value = ''
  pendingPoints.value = []
})

function toggleMainIndicator(value) {
  mainIndicators.value = mainIndicators.value.includes(value)
    ? mainIndicators.value.filter(item => item !== value)
    : [...mainIndicators.value, value]
}

function selectDrawingTool(value) {
  drawingTool.value = drawingTool.value === value ? '' : value
  pendingPoints.value = []
}

async function addDrawingAnchor(point) {
  if (!drawingTool.value) return
  pendingPoints.value = [...pendingPoints.value, point]
  if (pendingPoints.value.length < drawingPointCount(drawingTool.value)) return
  try {
    await drawingStore.add(drawingTool.value, pendingPoints.value)
    message.success('绘图已保存')
    pendingPoints.value = []
    drawingTool.value = ''
  } catch (reason) {
    message.error(reason?.message || String(reason))
  }
}

async function removeDrawing(id) {
  try {
    await drawingStore.remove(id)
    message.success('绘图已软删除')
  } catch (reason) {
    message.error(reason?.message || String(reason))
  }
}

async function clearDrawings() {
  try {
    await drawingStore.clear()
    message.success('当前证券、周期和复权范围的绘图已软删除')
  } catch (reason) {
    message.error(reason?.message || String(reason))
  }
}
</script>

<template>
  <section class="professional-kline-panel">
    <n-flex align="center" justify="space-between" :wrap="true" class="chart-toolbar">
      <n-flex align="center" :wrap="true" :size="8">
        <n-select v-model:value="period" :options="periodOptions" size="small" style="width: 105px" aria-label="K线周期"/>
        <n-select v-model:value="adjustment" :options="adjustmentOptions" size="small" style="width: 96px" aria-label="复权方式"/>
        <n-button-group size="small">
          <n-button :type="viewMode === 'candle' ? 'primary' : 'default'" @click="viewMode = 'candle'">K线</n-button>
          <n-button :type="viewMode === 'line' ? 'primary' : 'default'" @click="viewMode = 'line'">分时</n-button>
        </n-button-group>
        <n-button size="small" :type="mainIndicators.includes('MA') ? 'primary' : 'default'" @click="toggleMainIndicator('MA')">MA</n-button>
        <n-button size="small" :type="mainIndicators.includes('BOLL') ? 'primary' : 'default'" @click="toggleMainIndicator('BOLL')">BOLL</n-button>
        <n-select v-model:value="subIndicator" :options="['VOL','MACD','KDJ','RSI'].map(value => ({label:value,value}))" size="small" style="width: 95px" aria-label="副图指标"/>
        <n-button size="small" @click="chartCanvas?.resetZoom()">复位</n-button>
      </n-flex>
      <n-flex v-if="showMicrostructure && !legacy" :size="8">
        <n-button size="small" secondary @click="microstructureVisible = true">竞价 / 分时 / 逐笔</n-button>
      </n-flex>
    </n-flex>

    <n-alert v-if="legacy" type="info" :bordered="false" class="chart-alert">
      境外行情沿用 legacy 兼容数据源并复用统一渲染核心；本版仅提供日线，不启用新绘图持久化和微观行情。
    </n-alert>
    <n-flex v-if="!compact && !legacy" align="center" :wrap="true" :size="6" class="drawing-toolbar">
      <n-text depth="3">绘图（版本 {{ drawingStore.document.value.revision }}）</n-text>
      <n-button
          v-for="tool in DRAWING_TOOLS"
          :key="tool.value"
          size="tiny"
          :type="drawingTool === tool.value ? 'warning' : 'default'"
          :disabled="drawingStore.loading.value || drawingStore.saving.value"
          @click="selectDrawingTool(tool.value)"
      >{{ tool.label }}</n-button>
      <n-button v-if="visibleDrawings.length" size="tiny" tertiary type="error" :loading="drawingStore.saving.value" @click="clearDrawings">全部软删除</n-button>
      <n-text v-if="drawingHint" type="warning">{{ drawingHint }}</n-text>
    </n-flex>

    <n-alert v-if="!legacy && drawingStore.conflict.value" type="warning" :bordered="false" class="chart-alert">{{ drawingStore.conflict.value }}</n-alert>
    <n-alert v-else-if="!legacy && drawingStore.error.value && !drawingStore.loading.value" type="warning" :bordered="false" class="chart-alert">
      绘图读取失败：{{ drawingStore.error.value }}；行情仍可查看。
    </n-alert>
    <ChartDataMeta :model="model" :loading="loading" :error="error" @refresh="refresh"/>
    <n-spin :show="loading && !model.bars.length" description="正在读取行情">
      <MarketChartCanvas
          ref="chartCanvas"
          :model="model"
          :config="chartConfig"
          :drawings="legacy ? [] : drawingStore.document.value.drawings"
          :drawing-tool="legacy ? '' : drawingTool"
          :height="chartHeight"
          @drawing-anchor="addDrawingAnchor"
      />
      <n-empty v-if="!loading && !model.bars.length" description="暂无可展示的行情数据" class="chart-empty"/>
    </n-spin>

    <n-flex v-if="!compact && !legacy && visibleDrawings.length" :wrap="true" :size="6" class="drawing-list">
      <n-tag v-for="item in visibleDrawings" :key="item.id" closable @close="removeDrawing(item.id)">
        {{ DRAWING_TOOLS.find(tool => tool.value === item.type)?.label || item.type }}
      </n-tag>
    </n-flex>

    <InstrumentMicrostructureDrawer
        v-if="showMicrostructure && !legacy"
        v-model:show="microstructureVisible"
        :code="code"
        :name="name"
        :asset-type="assetType"
        :market="instrument.market"
    />
  </section>
</template>

<style scoped>
.professional-kline-panel {
  width: 100%;
}

.chart-toolbar,
.drawing-toolbar,
.drawing-list {
  gap: 8px;
  margin-bottom: 8px;
}

.drawing-toolbar {
  padding: 6px 0;
}

.chart-alert {
  margin: 7px 0;
}

.chart-empty {
  min-height: 280px;
  justify-content: center;
}
</style>
