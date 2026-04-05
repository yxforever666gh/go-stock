<script setup>
import {onMounted, onBeforeMount, ref, watchEffect} from "vue";
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

const chartRef=ref();

function setChartData(chart) {
  //console.log("setChartData")
  GetStockMinutePriceLineData(stockCode, stockName).then(result => {
    //console.log("GetStockMinutePriceLineData",result)
    const priceData = result.priceData
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
  })
}
const chart =ref( null)

onMounted(() => {
  chart.value = echarts.init( document.getElementById('sparkLine'+stockCode));
  setChartData(chart.value);
})


watchEffect(() => {
  console.log(stockName,'lastPrice变化为:', lastPrice,lastPrice > openPrice)
  setChartData(chart.value);
})


</script>
<template>
<div style="height: 20px;width: 100%"  :id="'sparkLine'+stockCode">
</div>
</template>
