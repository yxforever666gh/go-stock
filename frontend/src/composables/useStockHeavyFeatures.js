import { Environment } from '../services/browser-runtime.mjs'
import {
  GetStockKLine,
  GetStockMinutePriceLineData,
  SaveImage,
  SaveWordFile,
} from '../services/app-api'

let echartsLoader
let html2canvasLoader
let asBlobLoader

function loadEcharts() {
  if (!echartsLoader) {
    echartsLoader = import('echarts')
  }
  return echartsLoader
}

async function loadHtml2canvas() {
  if (!html2canvasLoader) {
    html2canvasLoader = import('html2canvas')
  }
  const mod = await html2canvasLoader
  return mod.default
}

async function loadAsBlob() {
  if (!asBlobLoader) {
    asBlobLoader = import('html-docx-js-typescript')
  }
  const mod = await asBlobLoader
  return mod.asBlob
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

function getHtml(ref) {
  if (!ref.value) {
    console.error('mdPreviewRef is not yet available')
    return ''
  }
  return ref.value.$el?.innerHTML || ''
}

export function useStockHeavyFeatures({
  data,
  downColor,
  kLineChartRef,
  kLineChartRef2,
  mdPreviewRef,
  message,
  tipsRef,
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

  async function exportAnalysisAsCanvasImage(name) {
    const element = document.querySelector('.md-editor-preview')
    if (!element) {
      message.error('无法找到分析结果元素')
      return
    }

    const html2canvas = await loadHtml2canvas()
    const canvas = await html2canvas(element)
    const dataUrl = canvas.toDataURL('image/png')
    const base64 = dataUrl.replace(/^data:image\/png;base64,/, '')
    const result = await SaveImage(name, base64)
    message.success(result)
  }

  async function exportAnalysisAsImage(name, code) {
    const { platform } = await Environment()
    const element = document.querySelector('.md-editor-preview')
    if (!element) {
      message.error('无法找到分析结果元素')
      return
    }

    const html2canvas = await loadHtml2canvas()
    if (platform === 'windows') {
      const canvas = await html2canvas(element, {
        useCORS: true,
        scale: 2,
        allowTaint: true,
      })
      const link = document.createElement('a')
      link.href = canvas.toDataURL('image/png')
      link.download = `${name}[${code}]-ai-analysis-result.png`
      link.click()
      return
    }

    await exportAnalysisAsCanvasImage(name)
  }

  async function exportAnalysisAsWord() {
    const html = getHtml(mdPreviewRef)
    const tipsHtml = getHtml(tipsRef)
    const value = `
         ${html}
         <hr>
         <div style="font-size: 12px;color: red">
         ${tipsHtml}
          </div>
<br>
本报告由go-stock项目生成：
<p>
<a href="https://github.com/yxforever666gh/go-stock">
AI赋能股票分析：自选股行情获取，成本盈亏展示，涨跌报警推送，市场整体/个股情绪分析，K线技术指标分析等。数据全部保留在本地。支持DeepSeek，OpenAI， Ollama，LMStudio，AnythingLLM，硅基流动，火山方舟，阿里云百炼等平台或模型。
</a></p>
`
    const asBlob = await loadAsBlob()
    const blob = await asBlob(value, { orientation: 'portrait' })
    const { platform } = await Environment()
    if (platform === 'windows') {
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `${data.name}[${data.code}]-ai-analysis-result.docx`
      a.click()
      URL.revokeObjectURL(a.href)
      a.remove()
      return
    }

    const arrayBuffer = await blob.arrayBuffer()
    const uint8Array = new Uint8Array(arrayBuffer)
    const binary = uint8Array.reduce((acc, byte) => acc + String.fromCharCode(byte), '')
    const base64 = btoa(binary)
    const result = await SaveWordFile(`${data.name}[${data.code}]-ai-analysis-result.docx`, base64)
    message.success(result)
  }

  return {
    exportAnalysisAsCanvasImage,
    exportAnalysisAsImage,
    exportAnalysisAsWord,
    renderDailyKLine,
    renderMinuteChart,
  }
}
