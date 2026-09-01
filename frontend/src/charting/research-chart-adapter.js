import {chartModelFromEnvelope} from './chart-contract.js'
import {formatInteger, formatMoney, formatPercent, formatPrice} from '../utils/number-format.js'
import {tradingDaySeparatorIndexes, weightedExecutionPrice} from '../utils/research-trade-chart.js'

function finite(value, fallback = 0) {
  const number = Number(value)
  return Number.isFinite(number) ? number : fallback
}

function escapeHTML(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;')
}

function dateTime(value) {
  return String(value || '').replace('T', ' ').slice(0, 19)
}

function missingSessionIntervals(chartData) {
  const sessions = chartData?.sessions || []
  const sessionsByDate = new Map(sessions.map(item => [item.date, item]))
  const explicit = new Set((chartData?.missingSessions || []).filter(date => {
    const session = sessionsByDate.get(date)
    return !session || session.status === 'missing'
  }))
  sessions.filter(item => item.status === 'missing').forEach(item => explicit.add(item.date))
  const rangeFrom = chartData?.rangeFrom ? new Date(chartData.rangeFrom) : null
  const rangeTo = chartData?.rangeTo ? new Date(chartData.rangeTo) : null
  return [...explicit].map(date => {
    const sessionFrom = new Date(`${date}T09:30:00+08:00`)
    const sessionTo = new Date(`${date}T15:00:00+08:00`)
    const from = rangeFrom && !Number.isNaN(rangeFrom.getTime()) && rangeFrom > sessionFrom ? rangeFrom : sessionFrom
    const to = rangeTo && !Number.isNaN(rangeTo.getTime()) && rangeTo < sessionTo ? rangeTo : sessionTo
    return {
      from: from.toISOString(),
      to: to.toISOString(),
      reason: '交易日分钟数据缺失',
    }
  }).filter(item => new Date(item.from) <= new Date(item.to))
}

export function adaptResearchChart(chartData = {}) {
  const sources = [...new Set((chartData.bars || []).map(item => item.source).filter(Boolean))]
  const status = chartData.status === 'complete' ? 'ok' : (chartData.status === 'empty' ? 'unavailable' : 'partial')
  const model = chartModelFromEnvelope({
    data: {
      instrument: {assetType: 'stock', code: chartData.stockCode || ''},
      name: chartData.stockName || '',
      period: '1m',
      adjustment: 'none',
      bars: chartData.bars || [],
      missingIntervals: missingSessionIntervals(chartData),
    },
    source: sources,
    sources,
    asOf: chartData.quoteAt || chartData.refreshedAt || '',
    fetchedAt: chartData.refreshedAt || '',
    status,
    errors: chartData.providerErrors || [],
  })
  return {...model, raw: chartData}
}

function tradeMarkPoints(model, trades) {
  const categories = new Set(model.bars.map(item => item.time))
  return (trades || []).flatMap(item => {
    if (!item.markerAt || !categories.has(item.markerAt)) return []
    const isBuy = String(item.side).toLowerCase() === 'buy'
    return [{
      name: isBuy ? '买入' : '卖出',
      coord: [item.markerAt, finite(item.executionPrice)],
      value: isBuy ? 'B' : 'S',
      symbol: 'pin',
      symbolSize: 48,
      itemStyle: {color: isBuy ? '#d03050' : '#18a058'},
      label: {show: true, color: '#fff', fontWeight: 'bold', formatter: isBuy ? 'B' : 'S'},
      tooltip: {
        formatter: `${isBuy ? '买入' : '卖出'} ${escapeHTML(dateTime(item.tradedAt))}<br>成交价：${formatPrice(item.executionPrice)}<br>数量：${formatInteger(item.quantity)}<br>费用：${formatMoney(item.totalFees)}${item.markerSnapped ? '<br>标记已吸附至最近分钟柱' : ''}`,
      },
    }]
  })
}

function extremaMarkPoints(model) {
  if (!model.bars.length) return []
  const highest = model.bars.reduce((result, item) => item.high > result.high ? item : result)
  const lowest = model.bars.reduce((result, item) => item.low < result.low ? item : result)
  return [
    {name: '区间最高', coord: [highest.time, highest.high], value: formatPrice(highest.high), symbol: 'circle', symbolSize: 9, label: {show: true, position: 'top', color: '#d03050', formatter: '高 {c}'}},
    {name: '区间最低', coord: [lowest.time, lowest.low], value: formatPrice(lowest.low), symbol: 'circle', symbolSize: 9, label: {show: true, position: 'bottom', color: '#18a058', formatter: '低 {c}'}},
  ]
}

function tradingDayLines(model) {
  const categories = model.bars.map(item => item.time)
  return [...tradingDaySeparatorIndexes(categories)].map(index => ({
    name: '交易日分隔',
    xAxis: categories[index],
    label: {show: false},
    lineStyle: {color: '#6b7280', type: 'dashed', width: 1.2, opacity: 0.85},
  }))
}

export function researchChartOverlays(model, trades = [], {showPriceLines = false} = {}) {
  const chartData = model.raw || {}
  const lines = tradingDayLines(model)
  if (showPriceLines) {
    const latestPrice = finite(chartData.currentPrice, model.bars.at(-1)?.close)
    const buyPrice = weightedExecutionPrice(trades, 'buy')
    const sellPrice = weightedExecutionPrice(trades, 'sell')
    if (latestPrice > 0) lines.push({name: '最新价', yAxis: latestPrice, label: {formatter: `最新 ${formatPrice(latestPrice)}`}, lineStyle: {color: '#2080f0', type: 'dotted'}})
    if (buyPrice > 0) lines.push({name: '买入均价', yAxis: buyPrice, label: {formatter: `买入 ${formatPrice(buyPrice)}`}, lineStyle: {color: '#d03050', type: 'dashed'}})
    if (sellPrice > 0) lines.push({name: '卖出均价', yAxis: sellPrice, label: {formatter: `卖出 ${formatPrice(sellPrice)}`}, lineStyle: {color: '#18a058', type: 'dashed'}})
  }
  const sessions = chartData.sessions || []
  return {
    mainName: '研究复盘',
    mainMarkPoints: [...tradeMarkPoints(model, trades), ...extremaMarkPoints(model)],
    mainMarkLines: lines,
    tooltipLines(bar) {
      const previousClose = finite(sessions.find(item => item.date === bar.time.slice(0, 10))?.previousClose)
      const change = previousClose > 0 ? (bar.close - previousClose) / previousClose : null
      const raw = bar.raw || {}
      const barTime = new Date(bar.time).getTime()
      const profitAvailable = raw.netPnl !== null && raw.netPnl !== undefined && trades.some(item => {
        return String(item.side).toLowerCase() === 'buy' && new Date(item.tradedAt).getTime() <= barTime + 60000
      })
      return [
        change === null
          ? '涨跌幅：--'
          : `涨跌幅：<span style="color:${change >= 0 ? '#d03050' : '#18a058'}">${formatPercent(change)}</span>`,
        profitAvailable
          ? `预估净收益：<span style="color:${finite(raw.netPnl) >= 0 ? '#d03050' : '#18a058'}">${escapeHTML(formatMoney(raw.netPnl))}（${escapeHTML(formatPercent(raw.netYieldRate))}）</span>`
          : '预估净收益：--',
      ]
    },
  }
}
