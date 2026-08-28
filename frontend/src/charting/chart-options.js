import {calculateIndicators} from './indicators.js'
import {buildDrawingSeries} from './drawing-series.js'

const upColor = '#d03050'
const downColor = '#18a058'
const indicatorColors = ['#f0a020', '#2080f0', '#8a2be2', '#e6b800', '#4b9cd3']

function finite(value) {
  const number = Number(value)
  return Number.isFinite(number) ? number : 0
}

function escapeHTML(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;')
}

function dateLabel(value) {
  return String(value || '').replace('T', ' ').slice(0, 16)
}

function defaultTooltip(model, bar, overlays, index) {
  const extra = overlays?.tooltipLines?.(bar, index) || []
  const lines = [
    `<strong>${escapeHTML(dateLabel(bar.time))}</strong>`,
    `开盘：${bar.open.toFixed(3)}　最高：${bar.high.toFixed(3)}`,
    `最低：${bar.low.toFixed(3)}　收盘：${bar.close.toFixed(3)}`,
    `成交量：${Math.round(bar.volume).toLocaleString()}　成交额：${bar.amount.toFixed(2)}`,
    `来源：${escapeHTML(bar.source || model.source || '--')}`,
    ...extra,
  ]
  return lines.join('<br>')
}

function intervalIndexes(categories, interval) {
  const exactFrom = categories.indexOf(interval.from)
  const exactTo = categories.indexOf(interval.to)
  if (exactFrom >= 0 || exactTo >= 0) return [Math.max(0, exactFrom), exactTo >= 0 ? exactTo : exactFrom]
  const fromTime = new Date(interval.from).getTime()
  const toTime = new Date(interval.to).getTime()
  if (!Number.isFinite(fromTime) || !Number.isFinite(toTime)) return null
  const indexes = categories
    .map((value, index) => ({index, time: new Date(value).getTime()}))
    .filter(item => Number.isFinite(item.time) && item.time >= fromTime && item.time <= toTime)
  if (indexes.length) return [indexes[0].index, indexes.at(-1).index]
  const firstAfter = categories.findIndex(value => {
    const time = new Date(value).getTime()
    return Number.isFinite(time) && time > toTime
  })
  if (firstAfter > 0) return [firstAfter - 1, firstAfter]
  if (firstAfter === 0) return [0, 0]
  return categories.length ? [categories.length - 1, categories.length - 1] : null
}

function missingMarkAreas(model, categories) {
  return model.missingIntervals.flatMap(interval => {
    const indexes = intervalIndexes(categories, interval)
    if (!indexes) return []
    return [[
      {name: interval.reason || '数据缺口', xAxis: categories[indexes[0]], itemStyle: {color: 'rgba(240,160,32,.12)'}},
      {xAxis: categories[indexes[1]]},
    ]]
  })
}

function lineSeries(id, name, data, color, axis = 0) {
  return {
    id,
    name,
    type: 'line',
    data,
    xAxisIndex: axis,
    yAxisIndex: axis,
    showSymbol: false,
    connectNulls: false,
    smooth: false,
    lineStyle: {width: 1.2, color},
    itemStyle: {color},
  }
}

function subIndicatorSeries(kind, indicators, bars) {
  if (kind === 'MACD') {
    return [
      {
        id: 'indicator:macd:histogram', name: 'MACD', type: 'bar', xAxisIndex: 1, yAxisIndex: 1,
        data: indicators.macd.histogram,
        itemStyle: {color: params => finite(params.value) >= 0 ? upColor : downColor},
      },
      lineSeries('indicator:macd:dif', 'DIF', indicators.macd.dif, '#f0a020', 1),
      lineSeries('indicator:macd:dea', 'DEA', indicators.macd.dea, '#2080f0', 1),
    ]
  }
  if (kind === 'KDJ') {
    return [
      lineSeries('indicator:kdj:k', 'K', indicators.kdj.k, '#f0a020', 1),
      lineSeries('indicator:kdj:d', 'D', indicators.kdj.d, '#2080f0', 1),
      lineSeries('indicator:kdj:j', 'J', indicators.kdj.j, '#8a2be2', 1),
    ]
  }
  if (kind === 'RSI') {
    return Object.entries(indicators.rsi).map(([name, values], index) => lineSeries(`indicator:rsi:${name}`, name.toUpperCase(), values, indicatorColors[index], 1))
  }
  const volume = bars.map(item => [item.volume, item.close >= item.open ? 1 : -1])
  return [
    {
      id: 'indicator:vol', name: 'VOL', type: 'bar', xAxisIndex: 1, yAxisIndex: 1, data: volume,
      itemStyle: {color: params => params.value?.[1] >= 0 ? 'rgba(208,48,80,.68)' : 'rgba(24,160,88,.68)'},
    },
    lineSeries('indicator:vol:ma5', 'VOL MA5', indicators.vol.averages.ma5, '#f0a020', 1),
    lineSeries('indicator:vol:ma10', 'VOL MA10', indicators.vol.averages.ma10, '#2080f0', 1),
  ]
}

export function buildChartOption(model, config = {}, overlays = {}, drawings = []) {
  const bars = model?.bars || []
  const categories = bars.map(item => item.time)
  const indicators = calculateIndicators(bars, config)
  const viewMode = config.viewMode === 'line' ? 'line' : 'candle'
  const mainIndicators = new Set(config.mainIndicators || ['MA'])
  const subIndicator = String(config.subIndicator || 'VOL').toUpperCase()
  const initialVisibleBars = Math.max(1, Number(config.initialVisibleBars) || 120)
  const start = Math.max(0, 100 - Math.min(1, initialVisibleBars / Math.max(1, bars.length)) * 100)
  const mainData = viewMode === 'line'
    ? bars.map(item => item.close)
    : bars.map(item => [item.open, item.close, item.low, item.high])
  const mainSeries = {
    id: 'price:main',
    name: overlays.mainName || (viewMode === 'line' ? '分时' : 'K线'),
    type: viewMode === 'line' ? 'line' : 'candlestick',
    data: mainData,
    showSymbol: false,
    sampling: viewMode === 'line' ? 'lttb' : undefined,
    lineStyle: viewMode === 'line' ? {width: 1.4, color: '#2080f0'} : undefined,
    areaStyle: viewMode === 'line' ? {color: 'rgba(32,128,240,.08)'} : undefined,
    itemStyle: viewMode === 'candle' ? {color: upColor, color0: downColor, borderColor: upColor, borderColor0: downColor} : undefined,
    markPoint: {data: overlays.mainMarkPoints || []},
    markLine: {symbol: ['none', 'none'], silent: true, data: overlays.mainMarkLines || []},
    markArea: {silent: true, data: missingMarkAreas(model, categories)},
  }
  const mainOverlaySeries = []
  if (mainIndicators.has('MA')) {
    Object.entries(indicators.ma).forEach(([name, values], index) => {
      mainOverlaySeries.push(lineSeries(`indicator:ma:${name}`, name.toUpperCase(), values, indicatorColors[index]))
    })
  }
  if (mainIndicators.has('BOLL')) {
    mainOverlaySeries.push(
      lineSeries('indicator:boll:upper', 'BOLL UPPER', indicators.boll.upper, '#8a2be2'),
      lineSeries('indicator:boll:middle', 'BOLL MID', indicators.boll.middle, '#f0a020'),
      lineSeries('indicator:boll:lower', 'BOLL LOWER', indicators.boll.lower, '#8a2be2'),
    )
  }
  const series = [
    mainSeries,
    ...mainOverlaySeries,
    ...subIndicatorSeries(subIndicator, indicators, bars),
    ...(overlays.extraSeries || []),
    ...buildDrawingSeries(drawings),
  ]

  return {
    animation: false,
    darkMode: config.darkTheme === true,
    legend: {top: 0, right: 24, type: 'scroll'},
    grid: [
      {left: 64, right: 72, top: 36, height: '56%'},
      {left: 64, right: 72, top: '69%', height: '17%'},
    ],
    tooltip: {
      trigger: 'axis',
      confine: true,
      axisPointer: {type: 'cross'},
      formatter(params) {
        const row = Array.isArray(params) ? params.find(item => item.seriesId === 'price:main') : null
        const index = row?.dataIndex
        return Number.isInteger(index) && bars[index] ? defaultTooltip(model, bars[index], overlays, index) : ''
      },
    },
    axisPointer: {link: [{xAxisIndex: [0, 1]}], label: {backgroundColor: '#5c677d'}},
    xAxis: [
      {type: 'category', data: categories, boundaryGap: true, axisLabel: {formatter: dateLabel, hideOverlap: true}},
      {type: 'category', gridIndex: 1, data: categories, boundaryGap: true, axisLabel: {show: false}},
    ],
    yAxis: [
      {type: 'value', scale: true, position: 'right', axisLine: {show: true}, splitLine: {lineStyle: {type: 'dashed', opacity: 0.3}}},
      {type: 'value', scale: true, gridIndex: 1, position: 'right', splitNumber: 3, axisLine: {show: true}, splitLine: {show: false}},
    ],
    dataZoom: [
      {type: 'inside', xAxisIndex: [0, 1], start, end: 100, zoomOnMouseWheel: true, moveOnMouseMove: true},
      {type: 'slider', xAxisIndex: [0, 1], start, end: 100, height: 20, bottom: 4, brushSelect: true},
    ],
    series,
  }
}
