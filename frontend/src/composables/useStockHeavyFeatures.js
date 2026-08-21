import {
  GetStockKLine,
  GetStockMinutePriceLineData,
} from '../services/stocks-api'

let echartsLoader

function loadEcharts() {
  if (!echartsLoader) {
    echartsLoader = import('echarts')
  }
  return echartsLoader
}

function calculateMA(dayCount, values) {
  const result = []
  for (let i = 0, len = values.length; i < len; i++) {
    if (i < dayCount) {
      result.push('-')
      continue
    }
    let sum = 0
    for (let j = 0; j < dayCount; j++) {
      sum += +values[i - j][1]
    }
    result.push((sum / dayCount).toFixed(2))
  }
  return result
}

export function useStockHeavyFeatures({
  data,
  downColor,
  kLineChartRef,
  kLineChartRef2,
  upColor,
}) {
  async function renderMinuteChart(code, name) {
    data.name = name
    data.code = code
    const [{ init }, result] = await Promise.all([
      loadEcharts(),
      GetStockMinutePriceLineData(code, name),
    ])
    const chart = init(kLineChartRef2.value)
    const priceData = result.priceData
    const category = []
    const price = []
    const volume = []
    const volumeRate = []
    let openprice = priceData[0].price
    let closeprice = priceData[priceData.length - 1].price
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
      if (i > 0) {
        const delta = priceData[i].volume - priceData[i - 1].volume
        volumeRate.push(((delta - volume[i - 1]) / volume[i - 1] * 100).toFixed(2))
        volume.push(delta)
      } else {
        volume.push(priceData[i].volume)
        volumeRate.push(0)
      }
    }

    chart.setOption({
      title: {
        subtext: `[${result.date}] 开盘:${openprice} 最新:${closeprice} 最高:${max} 最低:${min}`,
        left: 'center',
        top: '10',
        textStyle: {
          color: data.darkTheme ? '#ccc' : '#456',
        },
      },
      legend: {
        data: ['股价', '成交量'],
        textStyle: {
          color: data.darkTheme ? '#ccc' : '#456',
        },
        right: 50,
      },
      darkMode: data.darkTheme,
      tooltip: {
        trigger: 'axis',
        axisPointer: {
          type: 'cross',
          animation: false,
          label: {
            backgroundColor: '#505765',
          },
        },
      },
      axisPointer: {
        link: [{ xAxisIndex: 'all' }],
        label: {
          backgroundColor: '#888',
        },
      },
      xAxis: [
        {
          type: 'category',
          data: category,
          axisLabel: {
            show: false,
          },
        },
        {
          gridIndex: 1,
          type: 'category',
          data: category,
        },
      ],
      grid: [
        {
          left: '8%',
          right: '8%',
          height: '50%',
        },
        {
          left: '8%',
          right: '8%',
          top: '70%',
          height: '15%',
        },
      ],
      yAxis: [
        {
          axisLine: {
            show: true,
          },
          splitLine: {
            show: false,
          },
          name: '股价',
          min: (min - min * 0.01).toFixed(2),
          max: (max + max * 0.01).toFixed(2),
          minInterval: 0.01,
          type: 'value',
        },
        {
          gridIndex: 1,
          axisLine: {
            show: true,
          },
          splitLine: {
            show: false,
          },
          name: '成交量',
          type: 'value',
        },
      ],
      visualMap: {
        type: 'piecewise',
        seriesIndex: 0,
        top: 0,
        left: 10,
        orient: 'horizontal',
        textStyle: {
          color: data.darkTheme ? '#fff' : '#456',
        },
        pieces: [
          {
            text: '低于开盘价',
            gt: 0,
            lte: openprice,
            color: '#31F113',
            textStyle: {
              color: data.darkTheme ? '#fff' : '#456',
            },
          },
          {
            text: '大于开盘价小于收盘价',
            gt: openprice,
            lte: closeprice,
            color: '#1651EF',
            textStyle: {
              color: data.darkTheme ? '#fff' : '#456',
            },
          },
          {
            text: '大于收盘价',
            gt: closeprice,
            color: '#AC3B2A',
            textStyle: {
              color: data.darkTheme ? '#fff' : '#456',
            },
          },
        ],
      },
      series: [
        {
          name: '股价',
          data: price,
          type: 'line',
          smooth: false,
          showSymbol: false,
          lineStyle: {
            width: 3,
          },
          markPoint: {
            symbol: 'arrow',
            symbolRotate: 90,
            symbolSize: [10, 20],
            symbolOffset: [10, 0],
            itemStyle: {
              color: '#FC290D',
            },
            label: {
              position: 'right',
            },
            data: [
              { type: 'max', name: 'Max' },
              { type: 'min', name: 'Min' },
            ],
          },
          markLine: {
            symbol: 'none',
            data: [
              { type: 'average', name: 'Average' },
              {
                lineStyle: {
                  color: '#FFCB00',
                  width: 0.5,
                },
                yAxis: openprice,
                name: '开盘价',
              },
              {
                yAxis: closeprice,
                symbol: 'none',
                lineStyle: {
                  color: 'red',
                  width: 0.5,
                },
              },
            ],
          },
        },
        {
          xAxisIndex: 1,
          yAxisIndex: 1,
          name: '成交量',
          data: volume,
          type: 'bar',
        },
      ],
    })
  }

  async function renderDailyKLine() {
    const [{ init }, result] = await Promise.all([
      loadEcharts(),
      GetStockKLine(data.code, data.name, 365),
    ])
    const chart = init(kLineChartRef.value)
    const categoryData = []
    const values = []
    const volumns = []
    for (let i = 0; i < result.length; i++) {
      const item = result[i]
      categoryData.push(item.day)
      const flag = item.close > item.open ? 1 : -1
      values.push([item.open, item.close, item.low, item.high])
      volumns.push([i, item.volume / 10000, flag])
    }

    chart.setOption({
      darkMode: data.darkTheme,
      animation: false,
      legend: {
        bottom: 10,
        left: 'center',
        data: ['日K', 'MA5', 'MA10', 'MA20', 'MA30'],
        textStyle: {
          color: data.darkTheme ? '#ccc' : '#456',
        },
      },
      tooltip: {
        trigger: 'axis',
        axisPointer: {
          type: 'cross',
          lineStyle: {
            color: '#376df4',
            width: 1,
            opacity: 1,
          },
        },
        borderWidth: 2,
        borderColor: data.darkTheme ? '#456' : '#ccc',
        backgroundColor: data.darkTheme ? '#456' : '#fff',
        padding: 10,
        textStyle: {
          color: data.darkTheme ? '#ccc' : '#456',
        },
        formatter(params) {
          const volum = params[5].data
          const ma5 = params[1].data
          const ma10 = params[2].data
          const ma20 = params[3].data
          const ma30 = params[4].data
          const current = params[0]
          const currentItemData = current.data

          return current.name + '<br>' +
            '开盘:' + currentItemData[1] + '<br>' +
            '收盘:' + currentItemData[2] + '<br>' +
            '最低:' + currentItemData[3] + '<br>' +
            '最高:' + currentItemData[4] + '<br>' +
            '成交量(万手):' + volum[1] + '<br>' +
            'MA5日均线:' + ma5 + '<br>' +
            'MA10日均线:' + ma10 + '<br>' +
            'MA20日均线:' + ma20 + '<br>' +
            'MA30日均线:' + ma30
        },
      },
      axisPointer: {
        link: [{ xAxisIndex: 'all' }],
        label: {
          backgroundColor: '#888',
        },
      },
      visualMap: {
        show: false,
        seriesIndex: 5,
        dimension: 2,
        pieces: [
          { value: -1, color: downColor },
          { value: 1, color: upColor },
        ],
      },
      grid: [
        {
          left: '10%',
          right: '8%',
          height: '50%',
        },
        {
          left: '10%',
          right: '8%',
          top: '63%',
          height: '16%',
        },
      ],
      xAxis: [
        {
          type: 'category',
          data: categoryData,
          boundaryGap: false,
          axisLine: { onZero: false },
          splitLine: { show: false },
          min: 'dataMin',
          max: 'dataMax',
          axisPointer: { z: 100 },
        },
        {
          type: 'category',
          gridIndex: 1,
          data: categoryData,
          boundaryGap: false,
          axisLine: { onZero: false },
          axisTick: { show: false },
          splitLine: { show: false },
          axisLabel: { show: false },
          min: 'dataMin',
          max: 'dataMax',
        },
      ],
      yAxis: [
        {
          scale: true,
          splitArea: { show: true },
        },
        {
          scale: true,
          gridIndex: 1,
          splitNumber: 2,
          axisLabel: { show: false },
          axisLine: { show: false },
          axisTick: { show: false },
          splitLine: { show: false },
        },
      ],
      dataZoom: [
        {
          type: 'inside',
          xAxisIndex: [0, 1],
          start: 86,
          end: 100,
        },
        {
          show: true,
          xAxisIndex: [0, 1],
          type: 'slider',
          top: '85%',
          start: 86,
          end: 100,
        },
      ],
      series: [
        {
          name: '日K',
          type: 'candlestick',
          data: values,
          itemStyle: {
            color: upColor,
            color0: downColor,
          },
          markPoint: {
            label: {
              formatter(param) {
                return param != null ? param.value + '' : ''
              },
            },
            data: [
              { name: '最高', type: 'max', valueDim: 'highest' },
              { name: '最低', type: 'min', valueDim: 'lowest' },
              { name: '平均收盘价', type: 'average', valueDim: 'close' },
            ],
            tooltip: {
              formatter(param) {
                return param.name + '<br>' + (param.data.coord || '')
              },
            },
          },
          markLine: {
            symbol: ['none', 'none'],
            data: [
              [
                {
                  name: 'from lowest to highest',
                  type: 'min',
                  valueDim: 'lowest',
                  symbol: 'circle',
                  symbolSize: 10,
                  label: { show: false },
                  emphasis: { label: { show: false } },
                },
                {
                  type: 'max',
                  valueDim: 'highest',
                  symbol: 'circle',
                  symbolSize: 10,
                  label: { show: false },
                  emphasis: { label: { show: false } },
                },
              ],
              { name: 'min line on close', type: 'min', valueDim: 'close' },
              { name: 'max line on close', type: 'max', valueDim: 'close' },
            ],
          },
        },
        {
          name: 'MA5',
          type: 'line',
          data: calculateMA(5, values),
          smooth: true,
          showSymbol: false,
          lineStyle: { opacity: 0.6 },
        },
        {
          name: 'MA10',
          type: 'line',
          data: calculateMA(10, values),
          smooth: true,
          showSymbol: false,
          lineStyle: { opacity: 0.6 },
        },
        {
          name: 'MA20',
          type: 'line',
          data: calculateMA(20, values),
          smooth: true,
          showSymbol: false,
          lineStyle: { opacity: 0.6 },
        },
        {
          name: 'MA30',
          type: 'line',
          data: calculateMA(30, values),
          smooth: true,
          showSymbol: false,
          lineStyle: { opacity: 0.6 },
        },
        {
          name: '成交量(手)',
          type: 'bar',
          xAxisIndex: 1,
          yAxisIndex: 1,
          itemStyle: { color: '#7fbe9e' },
          data: volumns,
        },
      ],
    })
    chart.on('click', { seriesName: '日K' }, function () {})
  }

  return {
    renderDailyKLine,
    renderMinuteChart,
  }
}
