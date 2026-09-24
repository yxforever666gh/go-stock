<script setup>
import * as echarts from 'echarts'
import {nextTick, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {formatPercent} from '../utils/number-format'

const props = defineProps({points: {type: Array, default: () => []}})
const element = ref(null)
let chart = null
let observer = null

function render() {
  if (!chart || chart.isDisposed()) return
  if (!props.points.length) {
    chart.clear()
    return
  }
  chart.setOption({
    animation: false,
    grid: {left: 68, right: 24, top: 24, bottom: 42},
    tooltip: {trigger: 'axis', formatter: params => {
      const item = params?.[0]
      const point = props.points[item?.dataIndex]
      return point ? `${point.tradingDate}<br>综合TWR：${formatPercent(point.returnRate)}<br>有效账户：${point.effectiveAccountCount}` : ''
    }},
    xAxis: {type: 'category', data: props.points.map(item => item.tradingDate), axisLabel: {hideOverlap: true}},
    yAxis: {type: 'value', scale: true, axisLabel: {formatter: value => formatPercent(Number(value))}, splitLine: {lineStyle: {type: 'dashed', opacity: 0.35}}},
    series: [{name: '综合TWR', type: 'line', showSymbol: props.points.length < 40, data: props.points.map(item => item.returnRate), lineStyle: {width: 2, color: '#2080f0'}, itemStyle: {color: '#2080f0'}, areaStyle: {color: 'rgba(32,128,240,.08)'}}],
  }, {notMerge: true})
}

watch(() => props.points, async () => { await nextTick(); render() }, {deep: true})
onMounted(() => {
  if (!element.value) return
  chart = echarts.init(element.value, undefined, {renderer: 'canvas'})
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(element.value)
  render()
})
onBeforeUnmount(() => {
  observer?.disconnect()
  if (chart && !chart.isDisposed()) chart.dispose()
  chart = null
})
</script>

<template>
  <div v-if="points.length" ref="element" class="portfolio-return-chart" role="img" aria-label="股票预测综合收益率日曲线"/>
  <n-empty v-else description="所选范围暂无完整收益率曲线" class="portfolio-return-empty"/>
</template>

<style scoped>
.portfolio-return-chart, .portfolio-return-empty { width: 100%; height: 300px; min-height: 300px; }
.portfolio-return-empty { display: flex; justify-content: center; }
</style>
