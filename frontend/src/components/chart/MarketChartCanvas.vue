<script setup>
import * as echarts from 'echarts'
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {buildChartOption} from '../../charting/chart-options.js'

const props = defineProps({
  model: {type: Object, default: () => ({bars: [], missingIntervals: []})},
  config: {type: Object, default: () => ({})},
  overlays: {type: Object, default: () => ({})},
  drawings: {type: Array, default: () => []},
  drawingTool: {type: String, default: ''},
  height: {type: Number, default: 520},
})
const emit = defineEmits(['drawing-anchor'])

const chartElement = ref(null)
const option = computed(() => buildChartOption(props.model, props.config, props.overlays, props.drawings))
const chartIdentity = computed(() => {
  const instrument = props.model?.instrument || {}
  return [instrument.assetType, instrument.market, instrument.code, props.model?.period, props.model?.adjustment].join('|')
})
let chart = null
let resizeObserver = null
let previousIdentity = ''

function currentZoom() {
  const zoom = chart?.getOption()?.dataZoom
  if (!Array.isArray(zoom) || !zoom.length) return null
  return {start: zoom[0].start, end: zoom[0].end, startValue: zoom[0].startValue, endValue: zoom[0].endValue}
}

function restoreZoom(zoom) {
  if (!zoom || !chart) return
  chart.dispatchAction({type: 'dataZoom', dataZoomIndex: 0, ...zoom})
  chart.dispatchAction({type: 'dataZoom', dataZoomIndex: 1, ...zoom})
}

function render() {
  if (!chart || chart.isDisposed()) return
  if (!props.model?.bars?.length) {
    chart.clear()
    return
  }
  const sameIdentity = previousIdentity === chartIdentity.value
  const zoom = sameIdentity ? currentZoom() : null
  chart.setOption(option.value, {notMerge: true, lazyUpdate: true})
  previousIdentity = chartIdentity.value
  if (zoom) restoreZoom(zoom)
}

function anchorFromEvent(event) {
  if (!props.drawingTool || !chart || chart.isDisposed()) return
  const coordinate = chart.convertFromPixel({xAxisIndex: 0, yAxisIndex: 0}, [event.offsetX, event.offsetY])
  if (!Array.isArray(coordinate)) return
  const categories = props.model?.bars?.map(item => item.time) || []
  const categoryValue = coordinate[0]
  const index = typeof categoryValue === 'number' ? Math.round(categoryValue) : categories.indexOf(String(categoryValue))
  const time = categories[index]
  const value = Number(coordinate[1])
  if (time && Number.isFinite(value)) emit('drawing-anchor', {time, value})
}

function resetZoom() {
  chart?.dispatchAction({type: 'dataZoom', dataZoomIndex: 0, start: 0, end: 100})
  chart?.dispatchAction({type: 'dataZoom', dataZoomIndex: 1, start: 0, end: 100})
}

function zoomToTimeRange(from, to) {
  const categories = props.model?.bars?.map(item => item.time) || []
  const indexes = categories
    .map((time, index) => ({time: String(time), index}))
    .filter(item => item.time >= String(from) && item.time <= String(to))
  if (!indexes.length) return false
  const range = {startValue: indexes[0].index, endValue: indexes.at(-1).index}
  chart?.dispatchAction({type: 'dataZoom', dataZoomIndex: 0, ...range})
  chart?.dispatchAction({type: 'dataZoom', dataZoomIndex: 1, ...range})
  return true
}

watch([option, chartIdentity], async () => {
  await nextTick()
  render()
})

onMounted(() => {
  if (!chartElement.value) return
  chart = echarts.init(chartElement.value, undefined, {renderer: 'canvas'})
  chart.getZr().on('click', anchorFromEvent)
  resizeObserver = new ResizeObserver(() => chart?.resize())
  resizeObserver.observe(chartElement.value)
  render()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  if (chart && !chart.isDisposed()) {
    chart.getZr().off('click', anchorFromEvent)
    chart.dispose()
  }
  chart = null
})

defineExpose({resetZoom, zoomToTimeRange})
</script>

<template>
  <div
      ref="chartElement"
      class="market-chart-canvas"
      :class="{'is-drawing': drawingTool}"
      :style="{height: height + 'px', minHeight: height + 'px'}"
      role="img"
      :aria-label="(model?.name || model?.instrument?.code || '证券') + '行情图表'"
  />
</template>

<style scoped>
.market-chart-canvas {
  width: 100%;
}

.market-chart-canvas.is-drawing {
  cursor: crosshair;
}
</style>
