export const CHART_PERIODS = Object.freeze([
  {value: '1m', label: '1分钟'},
  {value: '5m', label: '5分钟'},
  {value: '15m', label: '15分钟'},
  {value: '30m', label: '30分钟'},
  {value: '60m', label: '60分钟'},
  {value: 'day', label: '日'},
  {value: 'week', label: '周'},
  {value: 'month', label: '月'},
  {value: 'quarter', label: '季'},
  {value: 'year', label: '年'},
])

export const CHART_ADJUSTMENTS = Object.freeze([
  {value: 'none', label: '不复权'},
  {value: 'qfq', label: '前复权'},
  {value: 'hfq', label: '后复权'},
])

const periodValues = new Set(CHART_PERIODS.map(item => item.value))
const adjustmentValues = new Set(CHART_ADJUSTMENTS.map(item => item.value))

function finiteNumber(value) {
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

function firstValue(object, keys) {
  for (const key of keys) {
    if (object?.[key] !== undefined && object?.[key] !== null && object?.[key] !== '') return object[key]
  }
  return undefined
}

export function defaultChartAdjustment(assetType) {
  return String(assetType || '').toLowerCase() === 'stock' ? 'qfq' : 'none'
}

export function normalizePeriod(value, fallback = 'day') {
  const period = String(value || '').trim().toLowerCase()
  return periodValues.has(period) ? period : fallback
}

export function normalizeAdjustment(value, assetType = 'stock') {
  const type = String(assetType || 'stock').trim().toLowerCase()
  if (type === 'index') return 'none'
  const adjustment = String(value || '').trim().toLowerCase()
  return adjustmentValues.has(adjustment) ? adjustment : defaultChartAdjustment(type)
}

export function normalizeInstrument(value = {}) {
  return {
    assetType: String(value.assetType || value.asset_type || 'stock').trim().toLowerCase(),
    market: String(value.market || '').trim().toUpperCase(),
    code: String(value.code || value.symbol || '').trim(),
  }
}

export function isLegacyChartInstrument(value = {}) {
  const instrument = normalizeInstrument(value)
  if (instrument.market && !['SH', 'SZ'].includes(instrument.market)) return true
  return /^(?:hk|us(?:\.|[A-Z])|gb_)/i.test(instrument.code)
}

export function drawingScopeKey({instrument, period, adjustment} = {}) {
  const normalized = normalizeInstrument(instrument)
  return [
    normalized.assetType,
    normalized.market || '-',
    normalized.code.toLowerCase(),
    normalizePeriod(period),
    normalizeAdjustment(adjustment, normalized.assetType),
  ].join('|')
}

export function normalizeChartBar(value = {}) {
  const time = String(firstValue(value, ['time', 'at', 'date', 'day']) || '').trim()
  const open = finiteNumber(firstValue(value, ['open', 'o']))
  const close = finiteNumber(firstValue(value, ['close', 'c', 'price']))
  const low = finiteNumber(firstValue(value, ['low', 'l']))
  const high = finiteNumber(firstValue(value, ['high', 'h']))
  if (!time || open === null || close === null || low === null || high === null) return null
  if (low > high) return null
  return {
    time,
    open,
    close,
    low,
    high,
    volume: finiteNumber(firstValue(value, ['volume', 'vol'])) ?? 0,
    amount: finiteNumber(firstValue(value, ['amount', 'turnover'])) ?? 0,
    source: String(value.source || '').trim(),
    raw: value,
  }
}

function comparableTime(value) {
  const milliseconds = new Date(value).getTime()
  return Number.isFinite(milliseconds) ? milliseconds : String(value)
}

export function normalizeChartBars(values = []) {
  const byTime = new Map()
  for (const value of Array.isArray(values) ? values : []) {
    const bar = normalizeChartBar(value)
    if (bar) byTime.set(bar.time, bar)
  }
  return [...byTime.values()].sort((left, right) => {
    const leftValue = comparableTime(left.time)
    const rightValue = comparableTime(right.time)
    if (typeof leftValue === 'number' && typeof rightValue === 'number') return leftValue - rightValue
    return String(leftValue).localeCompare(String(rightValue))
  })
}

export function normalizeMissingIntervals(values = []) {
  return (Array.isArray(values) ? values : []).flatMap(value => {
    if (typeof value === 'string') return [{from: value, to: value, reason: 'missing'}]
    const from = String(value?.from || value?.start || '').trim()
    const to = String(value?.to || value?.end || from).trim()
    if (!from || !to) return []
    return [{from, to, reason: String(value.reason || 'missing')}]
  })
}

export function chartModelFromEnvelope(envelope = {}, fallbackInstrument = {}) {
  const payload = envelope?.data && typeof envelope.data === 'object' ? envelope.data : {}
  const bars = normalizeChartBars(payload.bars || payload.items || [])
  const sources = Array.isArray(envelope.sources)
    ? envelope.sources
    : (Array.isArray(envelope.source) ? envelope.source : [envelope.source].filter(Boolean))
  return {
    instrument: normalizeInstrument(payload.instrument || fallbackInstrument),
    name: String(payload.name || payload.instrumentName || ''),
    period: normalizePeriod(payload.period),
    adjustment: normalizeAdjustment(payload.adjustment, (payload.instrument || fallbackInstrument).assetType),
    bars,
    missingIntervals: normalizeMissingIntervals(payload.missingIntervals || payload.missingRanges || []),
    status: String(envelope.status || (bars.length ? 'ok' : 'unavailable')),
    source: envelope.source || '',
    sources,
    asOf: String(envelope.asOf || ''),
    fetchedAt: String(envelope.fetchedAt || ''),
    errors: Array.isArray(envelope.errors) ? envelope.errors : [],
    meta: envelope.meta || {},
    raw: payload,
  }
}
