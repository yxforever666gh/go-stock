<script setup>
import {onBeforeUnmount, onMounted, ref, watch} from "vue";
import * as echarts from 'echarts';
import {GetStockMinutePriceLineData} from "../services/app-api"; // 如果您使用多个组件，请将此样式导入放在您的主文件中
const {stockCode,stockName,lastPrice,openPrice,darkTheme} = defineProps({
  stockCode: {
    type: String,
    default: ""
  },
  stockName: {
    type: String,
    default: ""
  },
  lastPrice: {
    type: Number,
    default: 0
  },
  openPrice: {
    type: Number,
    default: 0
  },
  darkTheme: {
    type: Boolean,
    default: true
  },
})

const chartElement = ref(null)
let chart = null
let requestVersion = 0
let mounted = false

async function setChartData() {
  if (!mounted || !chart || chart.isDisposed()) return
  const version = ++requestVersion
  try {
    const result = await GetStockMinutePriceLineData(stockCode, stockName)
    if (!mounted || version !== requestVersion || !chart || chart.isDisposed()) return
    const priceData = Array.isArray(result?.priceData) ? result.priceData : []
    let category = []
    let price = []
    let min = 0
    let max = 0
    for (let i = 0; i < priceData.length; i++) {
      category.push(priceData[i].time)
      price.push(priceData[i].price)
      if (min === 0 || min > priceData[i].price) {
        min = priceData[i].price
      }
      if (max < priceData[i].price) {
        max = priceData[i].price
      }
    }
    let option = {
      padding: [0, 0, 0, 0],
      grid: {
        top: 0,
        left: 0,
        right: 0,
        bottom: 0
      },
      tooltip: {
        trigger: 'axis',
        axisPointer: {
          type: 'cross',
          label: {
            backgroundColor: '#6a7985'
          }
        }
      },
      xAxis: {
        show: false,
        type: 'category',
        data: category
      },
      yAxis: {
        show: false,
        type: 'value',
        min: (min).toFixed(2),
        max: (max).toFixed(2),
        minInterval: 0.01,
      },
      // visualMap: {
      //   show: false,
      //   type: 'piecewise',
      //   pieces: [
      //     {
      //       min: Number(min),
      //       max: Number(openPrice),
      //       color: 'green'
      //     },
      //     {
      //       min: Number(openPrice),
      //       max: Number(max),
      //       color: 'red'
      //     }
      //   ]
      // },
      series: [
        {
          data: price,
          type: 'line',
          smooth: false,
          stack: '总量',
          showSymbol: false,
          lineStyle: {
            color: lastPrice > openPrice ? 'rgba(245, 0, 0, 1)' : 'rgb(6,251,10)'
          },
          areaStyle: {
            color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{
              offset: 0,
              color: lastPrice > openPrice ? 'rgba(245, 0, 0, 1)' : 'rgba(6,251,10, 1)'
            }, {
              offset: 1,
              color: lastPrice > openPrice ? 'rgba(245, 0, 0, 0.25)' : 'rgba(6,251,10, 0.25)'
            }])
          },
        }
      ]
    };
    chart.setOption(option);
  } catch (_) {
    // Minute data is optional in compact detail charts. The parent page keeps
    // the rest of the recommendation detail usable when the source is absent.
  }
}

onMounted(() => {
  mounted = true
  if (chartElement.value) {
    chart = echarts.init(chartElement.value)
    void setChartData()
  }
})

watch(() => [stockCode, stockName, lastPrice, openPrice, darkTheme], () => {
  void setChartData()
}, {flush: 'post'})

onBeforeUnmount(() => {
  mounted = false
  requestVersion++
  if (chart && !chart.isDisposed()) chart.dispose()
  chart = null
})


</script>
<template>
<div ref="chartElement" style="height: 20px;width: 100%">
</div>
</template>
