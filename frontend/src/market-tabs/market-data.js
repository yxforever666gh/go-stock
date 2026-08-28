export function rowsFrom(data) {
  if (Array.isArray(data)) return data
  for (const key of ['rows', 'items', 'list', 'snapshots']) {
    if (Array.isArray(data?.[key])) return data[key]
  }
  return []
}

export function firstValue(row, keys, fallback = undefined) {
  for (const key of keys) {
    if (row?.[key] !== undefined && row?.[key] !== null && row?.[key] !== '') return row[key]
  }
  return fallback
}

export function numberValue(row, keys, fallback = 0) {
  const value = Number(firstValue(row, keys, fallback))
  return Number.isFinite(value) ? value : fallback
}

export function dateValue(row) {
  return String(firstValue(row, ['tradeDate', 'date', 'day', 'at', 'time', 'snapTime'], ''))
}

export function itemCode(row, index = 0) {
  return String(firstValue(row, ['code', 'sectorCode', 'conceptCode', 'stockCode', 'symbol'], `row-${index}`))
}

export function itemName(row) {
  return String(firstValue(row, ['name', 'sectorName', 'conceptName', 'stockName', 'symbolName'], '--'))
}

export function historyFrom(row) {
  const history = firstValue(row, ['history', 'trend', 'points'], [])
  return Array.isArray(history) ? history : []
}

export function optionalNumberValue(row, keys) {
  for (const key of keys) {
    if (!Object.prototype.hasOwnProperty.call(row || {}, key)) continue
    const raw = row[key]
    if (raw === null || raw === undefined || raw === '') return null
    const value = Number(raw)
    return Number.isFinite(value) ? value : null
  }
  return null
}

export function formatOptionalMetric(row, keys, {digits = null, signed = false, suffix = ''} = {}) {
  const value = optionalNumberValue(row, keys)
  if (value === null) return '—'
  const text = digits === null ? value.toLocaleString() : value.toFixed(digits)
  return `${signed && value > 0 ? '+' : ''}${text}${suffix}`
}

export function auctionSummaryFrom(data) {
  if (!data || typeof data !== 'object' || Array.isArray(data)) return {}
  return data.finalSnapshot || data.summary || rowsFrom(data).at(-1) || {}
}

export function normalizeFuturesPositionRows(data) {
  return rowsFrom(data).map(row => ({
    ...row,
    _date: dateValue(row).slice(0, 10),
    _settlePrice: numberValue(row, ['settlePrice', 'settle_price']),
    _long: numberValue(row, ['longPosition', 'long_position', 'long']),
    _longChange: numberValue(row, ['longChange', 'long_change']),
    _short: numberValue(row, ['shortPosition', 'short_position', 'short']),
    _shortChange: numberValue(row, ['shortChange', 'short_change']),
    _net: numberValue(row, ['netPosition', 'net_position', 'net']),
    _indexClose: numberValue(row, ['indexClose', 'index_close']),
    _indexChange: numberValue(row, ['indexChange', 'index_change']),
    _basis: numberValue(row, ['basis']),
  })).filter(row => row._date).sort((left, right) => left._date.localeCompare(right._date))
}

export function latestDatedRows(rows, {single = false} = {}) {
  const values = Array.isArray(rows) ? rows : []
  const latestDate = values.reduce((latest, row) => {
    const date = String(row?._date || dateValue(row)).slice(0, 10)
    return date > latest ? date : latest
  }, '')
  if (!latestDate) return []
  const latest = values.filter(row => String(row?._date || dateValue(row)).slice(0, 10) === latestDate)
  return single ? latest.slice(0, 1) : latest
}
