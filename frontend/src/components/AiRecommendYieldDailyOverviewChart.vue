<script setup>
import {computed, nextTick, onBeforeUnmount, onMounted, ref, watch} from "vue";
import * as echarts from "echarts";

const props = defineProps({
  overviewData: {
    type: Object,
    default: null
  },
  mode: {
    type: String,
    default: 'cumulative'
  },
  rateHeight: {
    type: Number,
    default: 320
  },
  amountHeight: {
    type: Number,
    default: 260
  }
})

const rateChartRef = ref(null)
const amountChartRef = ref(null)
let rateChartInstance = null
let amountChartInstance = null

const hasPoints = computed(() => Array.isArray(props.overviewData?.points) && props.overviewData.points.length > 0)
const isDailyMode = computed(() => props.mode === 'daily')

onMounted(() => {
  window.addEventListener('resize', handleResize)
  renderCharts()
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', handleResize)
  disposeCharts()
})

watch(() => [props.overviewData, props.mode], () => {
  renderCharts()
}, { deep: true })

function handleResize() {
  if (rateChartInstance) {
    rateChartInstance.resize()
  }
  if (amountChartInstance) {
    amountChartInstance.resize()
  }
}

function disposeCharts() {
  if (rateChartInstance) {
    rateChartInstance.dispose()
    rateChartInstance = null
  }
  if (amountChartInstance) {
    amountChartInstance.dispose()
    amountChartInstance = null
  }
}

function formatMoney(value) {
  const number = Number(value)
  if (!Number.isFinite(number)) {
    return '--'
  }
  return new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  }).format(number)
}

function formatPercent(value) {
  const number = Number(value)
  if (!Number.isFinite(number)) {
    return '--'
  }
  const prefix = number > 0 ? '+' : ''
  return `${prefix}${number.toFixed(2)}%`
}

function buildSharedCategories() {
  const points = Array.isArray(props.overviewData?.points) ? props.overviewData.points : []
  return points.map((item) => item.tradeDate)
}

function resolveDisplayedCost(point) {
  if (isDailyMode.value) {
    return Number(point?.dailyHoldingCostNet) || 0
  }
  return Number(point?.costBasisNet) || 0
}

function buildRateTooltip(params, points) {
  const rows = Array.isArray(params) ? params : [params]
  const point = points[rows?.[0]?.dataIndex]
  if (!point) {
    return rows?.[0]?.axisValueLabel || ''
  }
  const isDaily = isDailyMode.value
  const strategyRate = isDaily ? point.dailyYieldRate : point.cumulativeYieldRate
  const benchmarkRate = isDaily ? point.benchmarkDailyRate : point.benchmarkCumulativeRate
  const excessRate = isDaily ? point.excessDailyRate : point.excessCumulativeRate
  const amount = isDaily ? point.dailyAmountChange : point.cumulativeAmountChange
  const displayedCost = resolveDisplayedCost(point)
  const lines = [
    point.tradeDate,
    `${isDaily ? '策略单日收益率' : '策略累计收益率'}: ${formatPercent(strategyRate)}`,
    `${isDaily ? '基准单日收益率' : '基准累计收益率'}: ${formatPercent(benchmarkRate)}`,
    `${isDaily ? '超额单日收益率' : '超额累计收益率'}: ${formatPercent(excessRate)}`,
    `${isDaily ? '当日盈亏金额' : '累计盈亏金额'}: ${formatMoney(amount)}`,
    `${isDaily ? '当日持仓成本' : '组合净买入'}: ${formatMoney(displayedCost)}`,
    `持仓数: ${Number(point.holdingCount || 0)}`
  ]
  return lines.join('<br/>')
}

function buildAmountTooltip(params, points) {
  const rows = Array.isArray(params) ? params : [params]
  const point = points[rows?.[0]?.dataIndex]
  if (!point) {
    return rows?.[0]?.axisValueLabel || ''
  }
  const isDaily = isDailyMode.value
  const amount = isDaily ? point.dailyAmountChange : point.cumulativeAmountChange
  const benchmarkAmount = isDaily ? point.benchmarkDailyAmountChange : point.benchmarkCumulativeAmountChange
  const excessAmount = isDaily ? point.excessDailyAmountChange : point.excessCumulativeAmountChange
  const displayedCost = resolveDisplayedCost(point)
  const lines = [
    point.tradeDate,
    `${isDaily ? '策略当日盈亏金额' : '策略累计盈亏金额'}: ${formatMoney(amount)}`,
    `${isDaily ? '基准当日盈亏金额' : '基准累计盈亏金额'}: ${formatMoney(benchmarkAmount)}`,
    `${isDaily ? '超额当日盈亏金额' : '超额累计盈亏金额'}: ${formatMoney(excessAmount)}`,
    `${isDaily ? '策略单日收益率' : '策略累计收益率'}: ${formatPercent(isDaily ? point.dailyYieldRate : point.cumulativeYieldRate)}`,
    `${isDaily ? '当日持仓成本' : '组合净买入'}: ${formatMoney(displayedCost)}`,
    `持仓数: ${Number(point.holdingCount || 0)}`
  ]
  return lines.join('<br/>')
}

function buildRateOption() {
  const points = Array.isArray(props.overviewData?.points) ? props.overviewData.points : []
  const categories = buildSharedCategories()
  const strategyValues = points.map((item) => Number(isDailyMode.value ? item.dailyYieldRate : item.cumulativeYieldRate) || 0)
  const benchmarkValues = points.map((item) => Number(isDailyMode.value ? item.benchmarkDailyRate : item.benchmarkCumulativeRate) || 0)
  const excessValues = points.map((item) => Number(isDailyMode.value ? item.excessDailyRate : item.excessCumulativeRate) || 0)
  const benchmarkName = String(props.overviewData?.benchmarkName || '沪深300ETF（510300.SH，现金流匹配，已扣成本）').trim()

  return {
    animation: false,
    backgroundColor: '#ffffff',
    color: ['#b91c1c', '#2563eb', '#d97706'],
    title: {
      text: isDailyMode.value ? '收益率走势' : '累计收益率走势',
      left: 12,
      top: 8,
      textStyle: {
        color: '#334155',
        fontSize: 14,
        fontWeight: 600
      }
    },
    legend: {
      top: 8,
      right: 20,
      textStyle: {
        color: '#4b5563'
      }
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: {
        type: 'cross'
      },
      formatter: (params) => buildRateTooltip(params, points)
    },
    grid: {
      left: 64,
      right: 24,
      top: 52,
      bottom: 72
    },
    dataZoom: [
      {
        type: 'inside',
        start: 0,
        end: 100
      },
      {
        type: 'slider',
        bottom: 18,
        height: 22,
        start: 0,
        end: 100
      }
    ],
    xAxis: {
      type: 'category',
      data: categories,
      boundaryGap: false,
      axisLabel: {
        color: '#6b7280',
        hideOverlap: true
      },
      axisLine: {
        lineStyle: {
          color: '#d1d5db'
        }
      }
    },
    yAxis: {
      type: 'value',
      name: '收益率',
      axisLabel: {
        color: '#6b7280',
        formatter: (value) => `${Number(value).toFixed(2)}%`
      },
      splitLine: {
        lineStyle: {
          color: '#eef2f7'
        }
      }
    },
    series: [
      {
        name: isDailyMode.value ? '策略单日收益率' : '策略累计收益率',
        type: 'line',
        smooth: true,
        symbol: 'circle',
        symbolSize: 6,
        lineStyle: {
          width: 2.4
        },
        areaStyle: isDailyMode.value ? undefined : {
          color: 'rgba(185, 28, 28, 0.08)'
        },
        data: strategyValues
      },
      {
        name: isDailyMode.value ? `${benchmarkName}单日收益率` : `${benchmarkName}累计收益率`,
        type: 'line',
        smooth: true,
        symbol: 'circle',
        symbolSize: 5,
        lineStyle: {
          width: 2,
          type: 'dashed'
        },
        data: benchmarkValues
      },
      {
        name: isDailyMode.value ? '超额单日收益率' : '超额累计收益率',
        type: 'line',
        smooth: true,
        symbol: 'circle',
        symbolSize: 5,
        lineStyle: {
          width: 2,
          type: 'dotted'
        },
        data: excessValues
      }
    ]
  }
}

function buildAmountOption() {
  const points = Array.isArray(props.overviewData?.points) ? props.overviewData.points : []
  const categories = buildSharedCategories()
  const strategyAmountValues = points.map((item) => Number(isDailyMode.value ? item.dailyAmountChange : item.cumulativeAmountChange) || 0)
  const benchmarkAmountValues = points.map((item) => Number(isDailyMode.value ? item.benchmarkDailyAmountChange : item.benchmarkCumulativeAmountChange) || 0)
  const excessAmountValues = points.map((item) => Number(isDailyMode.value ? item.excessDailyAmountChange : item.excessCumulativeAmountChange) || 0)
  const benchmarkName = String(props.overviewData?.benchmarkName || '沪深300ETF（510300.SH，现金流匹配，已扣成本）').trim()

  return {
    animation: false,
    backgroundColor: '#ffffff',
    color: ['#0f766e', '#2563eb', '#d97706'],
    title: {
      text: isDailyMode.value ? '金额变化' : '累计金额变化',
      left: 12,
      top: 8,
      textStyle: {
        color: '#334155',
        fontSize: 14,
        fontWeight: 600
      }
    },
    legend: {
      top: 8,
      right: 20,
      textStyle: {
        color: '#4b5563'
      }
    },
    tooltip: {
      trigger: 'axis',
      axisPointer: {
        type: 'shadow'
      },
      formatter: (params) => buildAmountTooltip(params, points)
    },
    grid: {
      left: 64,
      right: 24,
      top: 52,
      bottom: 48
    },
    xAxis: {
      type: 'category',
      data: categories,
      boundaryGap: true,
      axisLabel: {
        color: '#6b7280',
        hideOverlap: true
      },
      axisLine: {
        lineStyle: {
          color: '#d1d5db'
        }
      }
    },
    yAxis: {
      type: 'value',
      name: '金额',
      axisLabel: {
        color: '#6b7280',
        formatter: (value) => formatMoney(value)
      },
      splitLine: {
        lineStyle: {
          color: '#eef2f7'
        }
      }
    },
    series: [
      {
        name: isDailyMode.value ? '策略当日盈亏金额' : '策略累计盈亏金额',
        type: isDailyMode.value ? 'bar' : 'line',
        smooth: !isDailyMode.value,
        symbol: isDailyMode.value ? undefined : 'circle',
        symbolSize: isDailyMode.value ? undefined : 6,
        lineStyle: isDailyMode.value ? undefined : {
          width: 2.2
        },
        areaStyle: isDailyMode.value ? undefined : {
          color: 'rgba(15, 118, 110, 0.08)'
        },
        barMaxWidth: isDailyMode.value ? 14 : undefined,
        itemStyle: isDailyMode.value ? {
          color: (params) => Number(params.value || 0) >= 0 ? '#0f766e' : '#0891b2'
        } : undefined,
        data: strategyAmountValues
      },
      {
        name: isDailyMode.value ? `${benchmarkName}当日盈亏金额` : `${benchmarkName}累计盈亏金额`,
        type: isDailyMode.value ? 'bar' : 'line',
        smooth: !isDailyMode.value,
        symbol: isDailyMode.value ? undefined : 'circle',
        symbolSize: isDailyMode.value ? undefined : 5,
        lineStyle: isDailyMode.value ? undefined : {
          width: 2,
          type: 'dashed'
        },
        barMaxWidth: isDailyMode.value ? 14 : undefined,
        itemStyle: isDailyMode.value ? {
          color: (params) => Number(params.value || 0) >= 0 ? '#2563eb' : '#1d4ed8'
        } : undefined,
        data: benchmarkAmountValues
      },
      {
        name: isDailyMode.value ? '超额当日盈亏金额' : '超额累计盈亏金额',
        type: isDailyMode.value ? 'bar' : 'line',
        smooth: !isDailyMode.value,
        symbol: isDailyMode.value ? undefined : 'circle',
        symbolSize: isDailyMode.value ? undefined : 5,
        lineStyle: isDailyMode.value ? undefined : {
          width: 2,
          type: 'dotted'
        },
        barMaxWidth: isDailyMode.value ? 14 : undefined,
        itemStyle: isDailyMode.value ? {
          color: (params) => Number(params.value || 0) >= 0 ? '#d97706' : '#b45309'
        } : undefined,
        data: excessAmountValues
      }
    ]
  }
}

function renderCharts() {
  if (!hasPoints.value) {
    disposeCharts()
    return
  }
  nextTick(() => {
    if (rateChartRef.value) {
      if (!rateChartInstance) {
        rateChartInstance = echarts.init(rateChartRef.value)
      }
      rateChartInstance.setOption(buildRateOption(), true)
      rateChartInstance.resize()
    }
    if (amountChartRef.value) {
      if (!amountChartInstance) {
        amountChartInstance = echarts.init(amountChartRef.value)
      }
      amountChartInstance.setOption(buildAmountOption(), true)
      amountChartInstance.resize()
    }
  })
}
</script>

<template>
  <div class="yield-daily-overview-shell">
    <template v-if="hasPoints">
      <div ref="rateChartRef" class="yield-daily-overview-canvas" :style="{ height: `${rateHeight}px` }"></div>
      <div ref="amountChartRef" class="yield-daily-overview-canvas yield-daily-overview-amount" :style="{ height: `${amountHeight}px` }"></div>
    </template>
    <div v-else class="yield-daily-overview-empty">
      <n-empty description="暂无可展示的全库日收益数据"></n-empty>
    </div>
  </div>
</template>

<style scoped>
.yield-daily-overview-shell {
  width: 100%;
  min-height: 320px;
}

.yield-daily-overview-canvas {
  width: 100%;
}

.yield-daily-overview-amount {
  margin-top: 14px;
}

.yield-daily-overview-empty {
  min-height: 320px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px dashed #d7dce6;
  border-radius: 12px;
  background: linear-gradient(180deg, #fbfcfe 0%, #f5f7fb 100%);
}
</style>
