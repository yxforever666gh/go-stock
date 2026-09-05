import {itemCode, itemName, optionalNumberValue, rowsFrom} from '../market-tabs/market-data.js'

export const fundFlowSortOptions = [
  {label: '主力净流入', value: 'netamount'},
  {label: '主力净占比', value: 'main_ratio'},
  {label: '超大单净流入', value: 'super_large_netamount'},
  {label: '大单净流入', value: 'large_netamount'},
  {label: '中单净流入', value: 'medium_netamount'},
  {label: '小单净流入', value: 'small_netamount'},
  {label: '涨跌幅', value: 'change_pct'},
]

export function normalizeFundFlowRows(data) {
  return rowsFrom(data).map((row, index) => ({
    ...row,
    _key: itemCode(row, index),
    _name: itemName(row),
    _netInflow: optionalNumberValue(row, ['netAmount', 'netInflow', 'net_inflow', 'mainNetInflow', 'main_net_inflow']),
    _mainNetRatio: optionalNumberValue(row, ['mainNetRatio', 'main_net_ratio']),
    _superLargeNetAmount: optionalNumberValue(row, ['superLargeNetAmount', 'super_large_net_amount']),
    _largeNetAmount: optionalNumberValue(row, ['largeNetAmount', 'large_net_amount']),
    _mediumNetAmount: optionalNumberValue(row, ['mediumNetAmount', 'medium_net_amount']),
    _smallNetAmount: optionalNumberValue(row, ['smallNetAmount', 'small_net_amount']),
    _changePercent: optionalNumberValue(row, ['changePercent', 'changePct', 'change_rate', 'pctChange']),
  }))
}

export function fundFlowTone(value) {
  const number = Number(value)
  if (!Number.isFinite(number) || number === 0) return 'default'
  return number > 0 ? 'error' : 'success'
}

export function formatFlowAmount(value) {
  const number = Number(value)
  if (value === null || value === undefined || !Number.isFinite(number)) return '—'
  const sign = number > 0 ? '+' : ''
  if (Math.abs(number) >= 100000000) return `${sign}${(number / 100000000).toFixed(2)} 亿`
  if (Math.abs(number) >= 10000) return `${sign}${(number / 10000).toFixed(2)} 万`
  return `${sign}${number.toFixed(2)}`
}

export function formatFlowPercent(value) {
  const number = Number(value)
  if (value === null || value === undefined || !Number.isFinite(number)) return '—'
  return `${number > 0 ? '+' : ''}${number.toFixed(2)}%`
}

export function compareOptional(left, right) {
  const leftNumber = left === null || left === undefined ? NaN : Number(left)
  const rightNumber = right === null || right === undefined ? NaN : Number(right)
  if (!Number.isFinite(leftNumber)) return Number.isFinite(rightNumber) ? -1 : 0
  if (!Number.isFinite(rightNumber)) return 1
  return leftNumber - rightNumber
}

export function fundFlowTradingDate(envelope, timelines = []) {
  const timelineDate = timelines.map(item => String(item?.data?.tradingDate || '')).filter(Boolean).sort().at(-1)
  if (timelineDate) return timelineDate
  const quoteDate = String(envelope?.asOf || '').match(/^\d{4}-\d{2}-\d{2}/)?.[0]
  return quoteDate && quoteDate !== '0001-01-01' ? quoteDate : '—'
}

export function limitedFundFlowSelection(keys, limit = 6) {
  return (Array.isArray(keys) ? keys : []).slice(0, Math.max(0, limit))
}
