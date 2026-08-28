export const FUND_CATEGORIES = Object.freeze([
  {label: '全部', value: 'all'},
  {label: '股票型', value: 'stock'},
  {label: '混合型', value: 'mixed'},
  {label: '债券型', value: 'bond'},
  {label: '指数型', value: 'index'},
  {label: 'QDII', value: 'qdii'},
  {label: 'FOF', value: 'fof'},
])

export const FUND_PERIODS = Object.freeze([
  {label: '日', value: 'day'},
  {label: '周', value: 'week'},
  {label: '月', value: 'month'},
  {label: '3月', value: '3m'},
  {label: '6月', value: '6m'},
  {label: '1年', value: '1y'},
  {label: '3年', value: '3y'},
  {label: '今年以来', value: 'ytd'},
  {label: '成立以来', value: 'since_inception'},
  {label: '规模', value: 'scale'},
])

export const ETF_CATEGORIES = Object.freeze([
  {label: '全部', value: 'all'},
  {label: '宽基', value: 'broad'},
  {label: '行业主题', value: 'industry'},
  {label: '跨境', value: 'cross_border'},
  {label: '债券', value: 'bond'},
  {label: '商品', value: 'commodity'},
  {label: '货币', value: 'money'},
])

export const ETF_SORT_OPTIONS = Object.freeze([
  {label: '涨跌幅', value: 'changeRate'},
  {label: '成交额', value: 'amount'},
  {label: '换手率', value: 'turnoverRate'},
  {label: '溢折价', value: 'premiumRate'},
  {label: '规模', value: 'scale'},
  {label: '资金净流入', value: 'netInflow'},
])

const FUND_PERIOD_FIELDS = Object.freeze({
  day: 'dayReturn',
  week: 'weekReturn',
  month: 'monthReturn',
  '3m': 'threeMonthReturn',
  '6m': 'sixMonthReturn',
  '1y': 'oneYearReturn',
  '3y': 'threeYearReturn',
  ytd: 'yearToDateReturn',
  since_inception: 'sinceInceptionReturn',
  scale: 'scale',
})

function objectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {}
}

function stringValue(value) {
  return value === undefined || value === null ? '' : String(value)
}

export function nullableNumber(value) {
  if (value === undefined || value === null || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

function itemsFrom(value) {
  if (Array.isArray(value)) return value
  const source = objectValue(value)
  if (Array.isArray(source.items)) return source.items
  if (Array.isArray(source.rows)) return source.rows
  if (Array.isArray(source.data)) return source.data
  if (source.data && typeof source.data === 'object') return itemsFrom(source.data)
  return []
}

export function normalizeFundRankingItem(value = {}) {
  const item = objectValue(value)
  return {
    ...item,
    code: stringValue(item.code),
    name: stringValue(item.name),
    category: stringValue(item.category || 'all'),
    nav: nullableNumber(item.nav),
    navDate: stringValue(item.navDate),
    dayReturn: nullableNumber(item.dayReturn),
    weekReturn: nullableNumber(item.weekReturn),
    monthReturn: nullableNumber(item.monthReturn),
    threeMonthReturn: nullableNumber(item.threeMonthReturn),
    sixMonthReturn: nullableNumber(item.sixMonthReturn),
    oneYearReturn: nullableNumber(item.oneYearReturn),
    threeYearReturn: nullableNumber(item.threeYearReturn),
    yearToDateReturn: nullableNumber(item.yearToDateReturn),
    sinceInceptionReturn: nullableNumber(item.sinceInceptionReturn),
    scale: nullableNumber(item.scale),
    scaleDate: stringValue(item.scaleDate),
    rank: nullableNumber(item.rank),
  }
}

export function normalizeFundRankingPage(value = {}) {
  const source = objectValue(value)
  const items = itemsFrom(source).map(normalizeFundRankingItem)
  return {
    items,
    total: Math.max(0, nullableNumber(source.total) ?? items.length),
    page: Math.max(1, nullableNumber(source.page) ?? 1),
    pageSize: Math.max(1, nullableNumber(source.pageSize) ?? 20),
    category: stringValue(source.category || 'all'),
    period: stringValue(source.period || 'day'),
    navDate: stringValue(source.navDate),
  }
}

export function fundPeriodMetric(item, period) {
  const field = FUND_PERIOD_FIELDS[period] || FUND_PERIOD_FIELDS.day
  return nullableNumber(item?.[field])
}

export function normalizeETFItem(value = {}) {
  const item = objectValue(value)
  return {
    ...item,
    code: stringValue(item.code),
    name: stringValue(item.name),
    market: stringValue(item.market).toUpperCase(),
    category: stringValue(item.category || 'all'),
    price: nullableNumber(item.price),
    changeRate: nullableNumber(item.changeRate),
    amount: nullableNumber(item.amount),
    turnoverRate: nullableNumber(item.turnoverRate),
    nav: nullableNumber(item.nav),
    navDate: stringValue(item.navDate),
    premiumRate: nullableNumber(item.premiumRate),
    shares: nullableNumber(item.shares),
    scale: nullableNumber(item.scale),
    netInflow: nullableNumber(item.netInflow),
    quoteTime: stringValue(item.quoteTime),
    rank: nullableNumber(item.rank),
  }
}

export function normalizeETFRankingPage(value = {}) {
  const source = objectValue(value)
  const items = itemsFrom(source).map(normalizeETFItem)
  return {
    items,
    total: Math.max(0, nullableNumber(source.total) ?? items.length),
    page: Math.max(1, nullableNumber(source.page) ?? 1),
    pageSize: Math.max(1, nullableNumber(source.pageSize) ?? 20),
    category: stringValue(source.category || 'all'),
    sort: stringValue(source.sort || 'changeRate'),
  }
}

export function normalizeETFSearchItems(value = {}) {
  return itemsFrom(value).map(normalizeETFItem)
}

export function normalizeETFDetail(value = {}) {
  const source = objectValue(value)
  const base = normalizeETFItem(source)
  const chart = objectValue(source.chartInstrument)
  return {
    ...base,
    trackingIndex: stringValue(source.trackingIndex),
    managementFee: nullableNumber(source.managementFee),
    listDate: stringValue(source.listDate),
    holdings: itemsFrom(source.holdings).map((holding, index) => ({
      ...objectValue(holding),
      code: stringValue(holding?.code),
      name: stringValue(holding?.name),
      weight: nullableNumber(holding?.weight),
      asOf: stringValue(holding?.asOf),
      _key: `${stringValue(holding?.code) || index}-${stringValue(holding?.asOf)}`,
    })).sort((left, right) => (right.weight ?? -Infinity) - (left.weight ?? -Infinity)),
    chartInstrument: {
      assetType: 'etf',
      market: stringValue(chart.market || base.market).toUpperCase(),
      code: stringValue(chart.code || base.code),
    },
  }
}

export function etfIdentity(value = {}) {
  const item = normalizeETFItem(value)
  return [item.market, item.code].filter(Boolean).join(':')
}
