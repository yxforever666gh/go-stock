<script setup>
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from "vue";
import * as echarts from "echarts";

const props = defineProps({
  chartData: {
    type: Object,
    default: null
  },
  height: {
    type: Number,
    default: 540
  }
})

const chartRef = ref(null)
let chartInstance = null

const hasRenderableBars = computed(() => {
  return Array.isArray(props.chartData?.bars) && props.chartData.bars.length > 0
})

onMounted(() => {
  window.addEventListener('resize', handleResize)
  renderChart()
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', handleResize)
  disposeChart()
})

watch(() => props.chartData, () => {
  renderChart()
}, {deep: true})

function handleResize() {
  if (chartInstance) {
    chartInstance.resize()
  }
}

function disposeChart() {
  if (!chartInstance) {
    return
  }
  chartInstance.dispose()
  chartInstance = null
}

function markerColor(type) {
  if (type === 'buy') {
    return '#18a058'
  }
  if (type === 'sell') {
    return '#d03050'
  }
  if (type === 'current') {
    return '#f0a020'
  }
  return '#2080f0'
}

function markerSymbol(type) {
  if (type === 'buy') {
    return 'diamond'
  }
  if (type === 'sell') {
    return 'circle'
  }
  if (type === 'current') {
    return 'roundRect'
  }
  return 'triangle'
}

const gapCategoryPrefix = '__gap__'

function parseTradeTimeToMs(value) {
  const text = String(value || '').trim()
  if (!text || text.startsWith(gapCategoryPrefix)) {
    return Number.NaN
  }
  const timestamp = Date.parse(text.replace(' ', 'T'))
  return Number.isFinite(timestamp) ? timestamp : Number.NaN
}

function isGapPoint(point) {
  return Boolean(point?.isGap)
}

function formatTradeDay(text) {
  return String(text || '').slice(5, 10)
}

function formatTradeClock(text) {
  return String(text || '').slice(11, 16)
}

function createGapPoint(prevBar, nextBar, slotIndex, slotCount, gapLabel) {
  return {
    tradeTime: `${gapCategoryPrefix}${prevBar.tradeTime}__${nextBar.tradeTime}__${slotIndex}`,
    gapLabel,
    gapSlotIndex: slotIndex,
    gapSlotCount: slotCount,
    isGap: true,
    open: null,
    high: null,
    low: null,
    close: null,
    volume: null,
    amount: null
  }
}

function buildGapLabel(prevBar, nextBar, diffMinutes) {
  const prevTime = String(prevBar?.tradeTime || '')
  const nextTime = String(nextBar?.tradeTime || '')
  const prevDay = formatTradeDay(prevTime)
  const nextDay = formatTradeDay(nextTime)
  const prevClock = formatTradeClock(prevTime)
  const nextClock = formatTradeClock(nextTime)
  if (prevDay && nextDay && prevDay !== nextDay) {
    return `${prevDay} 收盘后至 ${nextDay} 开盘`
  }
  if (prevClock === '11:30' && nextClock === '13:01') {
    return `${prevDay} 午休时段`
  }
  return `时间缺口 ${prevClock} -> ${nextClock}（约 ${Math.max(diffMinutes - 1, 1)} 分钟）`
}

function buildDisplayBars(bars) {
  const result = []
  bars.forEach((bar, index) => {
    if (index > 0) {
      const prevBar = bars[index - 1]
      const prevMs = parseTradeTimeToMs(prevBar.tradeTime)
      const currentMs = parseTradeTimeToMs(bar.tradeTime)
      const diffMinutes = Math.round((currentMs - prevMs) / 60000)
      if (Number.isFinite(diffMinutes) && diffMinutes > 1) {
        const prevDay = formatTradeDay(prevBar.tradeTime)
        const nextDay = formatTradeDay(bar.tradeTime)
        const prevClock = formatTradeClock(prevBar.tradeTime)
        const nextClock = formatTradeClock(bar.tradeTime)
        const isCrossDay = prevDay !== '' && nextDay !== '' && prevDay !== nextDay
        const isLunchBreak = prevClock === '11:30' && nextClock === '13:01'
        if (isCrossDay || isLunchBreak || diffMinutes >= 30) {
          const gapLabel = buildGapLabel(prevBar, bar, diffMinutes)
          const slotCount = isCrossDay ? 5 : isLunchBreak ? 3 : 2
          for (let slotIndex = 0; slotIndex < slotCount; slotIndex += 1) {
            result.push(createGapPoint(prevBar, bar, slotIndex, slotCount, gapLabel))
          }
        }
      }
    }
    result.push({
      ...bar,
      isGap: false,
      gapLabel: ''
    })
  })
  return result
}

function findPrevRealIndex(points, index) {
  for (let i = index - 1; i >= 0; i -= 1) {
    if (!isGapPoint(points[i])) {
      return i
    }
  }
  return -1
}

function findNextRealIndex(points, index) {
  for (let i = index + 1; i < points.length; i += 1) {
    if (!isGapPoint(points[i])) {
      return i
    }
  }
  return -1
}

function buildAxisLabelFormatter(points) {
  return (_, index) => {
    const point = points[index]
    if (!point || isGapPoint(point)) {
      return ''
    }
    const text = String(point.tradeTime || '')
    const currentDay = formatTradeDay(text)
    const currentTime = formatTradeClock(text)
    const prevRealIndex = findPrevRealIndex(points, index)
    const prevDay = prevRealIndex >= 0 ? formatTradeDay(points[prevRealIndex]?.tradeTime) : ''
    const nextRealIndex = findNextRealIndex(points, index)
    const nextDay = nextRealIndex >= 0 ? formatTradeDay(points[nextRealIndex]?.tradeTime) : ''
    const isDayChanged = prevRealIndex < 0 || currentDay !== prevDay
    const isDayEnding = nextRealIndex < 0 || currentDay !== nextDay
    if (isDayChanged || isDayEnding) {
      return `${currentDay}\n${currentTime}`
    }
    if (currentTime === '09:31' || currentTime === '13:01') {
      return currentTime
    }
    if (index % 10 === 0) {
      return currentTime
    }
    return ''
  }
}

function formatNumber(value, digits = 2) {
  const number = Number(value)
  if (!Number.isFinite(number)) {
    return '--'
  }
  return number.toFixed(digits)
}

function formatInteger(value) {
  const number = Number(value)
  if (!Number.isFinite(number)) {
    return '--'
  }
  if (number === 0) {
    return '0'
  }
  return new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: 0
  }).format(Math.round(number))
}

function buildTooltipFormatter(params, points) {
  const list = Array.isArray(params) ? params : [params]
  const dataIndex = list.find((item) => typeof item?.dataIndex === 'number')?.dataIndex
  if (typeof dataIndex !== 'number') {
    return list[0]?.axisValueLabel || ''
  }
  const bar = points[dataIndex]
  if (!bar) {
    return list[0]?.axisValueLabel || ''
  }
  if (isGapPoint(bar)) {
    return bar.gapLabel || '交易时段分隔'
  }
  const lines = [
    bar.tradeTime || '',
    `开: ${formatNumber(bar.open)}`,
    `收: ${formatNumber(bar.close)}`,
    `低: ${formatNumber(bar.low)}`,
    `高: ${formatNumber(bar.high)}`,
    `量: ${formatInteger(bar.volume)}`,
  ]

  const markerParams = list.filter((item) => item.seriesName === '信号标记')
  markerParams.forEach((item) => {
    const markerLabel = item?.data?.markerLabel || item.name || '标记'
    const markerStatus = item?.data?.markerStatus === 'approximated' ? '（近似）' : ''
    lines.push(`${markerLabel}: ${formatNumber(item.value?.[1])}${markerStatus}`)
  })

  return lines.join('<br/>')
}

function buildOption(chartData) {
  const bars = Array.isArray(chartData?.bars) ? chartData.bars : []
  const displayBars = buildDisplayBars(bars)
  const categories = displayBars.map((bar) => bar.tradeTime)
  const priceValues = displayBars.map((bar) => {
    if (isGapPoint(bar)) {
      return ['-', '-', '-', '-']
    }
    return [Number(bar.open), Number(bar.close), Number(bar.low), Number(bar.high)]
  })
  const volumeValues = displayBars.map((bar, index) => {
    if (isGapPoint(bar)) {
      return {
        value: [index, '-', 0],
        itemStyle: {
          color: 'rgba(0, 0, 0, 0)'
        }
      }
    }
    return [
      index,
      Number(bar.volume) > 0 ? Number(bar.volume) : 0,
      Number(bar.close) >= Number(bar.open) ? 1 : -1
    ]
  })
  const gapLineIndexes = displayBars
    .map((bar, index) => isGapPoint(bar) && bar.gapSlotIndex === 0 ? index : -1)
    .filter((index) => index >= 0)
  const gapLineIndexSet = new Set(gapLineIndexes)
  const markerIndexMap = new Map()
  displayBars.forEach((bar, index) => {
    if (!isGapPoint(bar)) {
      markerIndexMap.set(bar.tradeTime, index)
    }
  })
  const markerData = (Array.isArray(chartData?.markers) ? chartData.markers : [])
    .map((marker) => {
      const index = markerIndexMap.get(marker.time)
      if (typeof index !== 'number') {
        return null
      }
      return {
        name: marker.label,
        value: [index, Number(marker.price || 0)],
        markerLabel: marker.label,
        markerPrice: Number(marker.price || 0),
        markerStatus: marker.status,
        markerTime: marker.time,
        symbol: markerSymbol(marker.type),
        symbolSize: marker.type === 'sell' ? 14 : 16,
        symbolOffset: marker.type === 'sell' ? [0, -8] : [0, 0],
        itemStyle: {
          color: markerColor(marker.type),
          borderColor: '#ffffff',
          borderWidth: 1.5,
          shadowBlur: 6,
          shadowColor: 'rgba(0, 0, 0, 0.14)'
        },
        label: {
          show: true,
          formatter: marker.type === 'sell'
            ? `${marker.label}\n${formatNumber(Number(marker.price || 0))}`
            : marker.label,
          position: marker.type === 'sell' ? 'top' : 'bottom',
          offset: marker.type === 'sell' ? [0, -10] : [0, 8],
          color: markerColor(marker.type),
          fontWeight: 600,
          fontSize: 11
        }
      }
    })
    .filter(Boolean)

  return {
    animation: false,
    backgroundColor: '#ffffff',
    tooltip: {
      trigger: 'axis',
      axisPointer: {
        type: 'cross'
      },
      formatter: (params) => buildTooltipFormatter(params, displayBars)
    },
    axisPointer: {
      link: [
        {
          xAxisIndex: 'all'
        }
      ]
    },
    grid: [
      {
        left: 56,
        right: 28,
        top: 38,
        height: '54%'
      },
      {
        left: 56,
        right: 28,
        top: '72%',
        height: '18%'
      }
    ],
    dataZoom: [
      {
        type: 'inside',
        xAxisIndex: [0, 1],
        start: 0,
        end: 100
      },
      {
        type: 'slider',
        xAxisIndex: [0, 1],
        bottom: 10,
        height: 22,
        start: 0,
        end: 100
      }
    ],
    xAxis: [
      {
        type: 'category',
        data: categories,
        boundaryGap: true,
        axisLine: {
          lineStyle: {
            color: '#8b8f97'
          }
        },
        splitLine: {
          show: gapLineIndexes.length > 0,
          interval: (index) => gapLineIndexSet.has(index),
          lineStyle: {
            color: '#d7dce6',
            type: 'dashed'
          }
        },
        axisLabel: {
          color: '#5b6270',
          formatter: buildAxisLabelFormatter(displayBars),
          showMaxLabel: true,
          hideOverlap: false
        },
        min: 'dataMin',
        max: 'dataMax'
      },
      {
        gridIndex: 1,
        type: 'category',
        data: categories,
        boundaryGap: true,
        axisLine: {
          lineStyle: {
            color: '#8b8f97'
          }
        },
        splitLine: {
          show: gapLineIndexes.length > 0,
          interval: (index) => gapLineIndexSet.has(index),
          lineStyle: {
            color: '#d7dce6',
            type: 'dashed'
          }
        },
        axisLabel: {
          show: false
        },
        min: 'dataMin',
        max: 'dataMax'
      }
    ],
    yAxis: [
      {
        scale: true,
        splitArea: {
          show: false
        },
        splitLine: {
          lineStyle: {
            color: '#edf0f5'
          }
        },
        axisLabel: {
          color: '#5b6270'
        }
      },
      {
        gridIndex: 1,
        min: 0,
        splitNumber: 4,
        splitLine: {
          show: false
        },
        axisLabel: {
          color: '#5b6270',
          formatter: (value) => formatInteger(value)
        }
      }
    ],
    series: [
      {
        name: '价格',
        type: 'candlestick',
        data: priceValues,
        itemStyle: {
          color: '#d03050',
          color0: '#18a058',
          borderColor: '#d03050',
          borderColor0: '#18a058'
        }
      },
      {
        name: '信号标记',
        type: 'scatter',
        data: markerData,
        z: 10,
        tooltip: {
          trigger: 'item',
          formatter: (params) => {
            const marker = params?.data || {}
            const approx = marker.markerStatus === 'approximated' ? '（近似）' : ''
            return `${marker.markerLabel || params.name}<br/>${marker.markerTime || ''}<br/>价格: ${formatNumber(params.value?.[1])}${approx}`
          }
        }
      },
      {
        name: '成交量',
        type: 'bar',
        xAxisIndex: 1,
        yAxisIndex: 1,
        data: volumeValues,
        barMinHeight: 3,
        itemStyle: {
          color: (params) => {
            const rawValue = Array.isArray(params.data?.value) ? params.data.value : params.data
            if (rawValue?.[1] === '-') {
              return 'rgba(0, 0, 0, 0)'
            }
            return rawValue?.[2] >= 0 ? '#f3a6b5' : '#93d5b3'
          }
        }
      }
    ]
  }
}

function renderChart() {
  if (!hasRenderableBars.value) {
    disposeChart()
    return
  }

  nextTick(() => {
    if (!chartRef.value) {
      return
    }
    if (!chartInstance) {
      chartInstance = echarts.init(chartRef.value)
    }
    chartInstance.setOption(buildOption(props.chartData), true)
    chartInstance.resize()
  })
}
</script>

<template>
  <div class="yield-replay-chart-shell">
    <div v-if="hasRenderableBars" ref="chartRef" class="yield-replay-chart-canvas" :style="{ height: `${height}px` }"></div>
    <div v-else class="yield-replay-chart-empty">
      <n-empty description="当前记录没有可展示的分钟线"></n-empty>
    </div>
  </div>
</template>

<style scoped>
.yield-replay-chart-shell {
  width: 100%;
  min-height: 320px;
}

.yield-replay-chart-canvas {
  width: 100%;
}

.yield-replay-chart-empty {
  min-height: 320px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(180deg, #fbfcfe 0%, #f5f7fb 100%);
  border: 1px dashed #d7dce6;
  border-radius: 12px;
}
</style>
