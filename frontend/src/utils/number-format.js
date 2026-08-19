const formatterCache = new Map()

function finite(value) {
  const number = Number(value)
  return Number.isFinite(number) ? number : 0
}

function formatter(minimumFractionDigits, maximumFractionDigits) {
  const key = `${minimumFractionDigits}:${maximumFractionDigits}`
  if (!formatterCache.has(key)) {
    formatterCache.set(key, new Intl.NumberFormat('en-US', {
      useGrouping: true,
      minimumFractionDigits,
      maximumFractionDigits,
    }))
  }
  return formatterCache.get(key)
}

export function formatNumber(value, fractionDigits = 2) {
  return formatter(fractionDigits, fractionDigits).format(finite(value))
}

export function formatInteger(value) {
  return formatNumber(value, 0)
}

export function formatPrice(value) {
  return formatNumber(value, 3)
}

export function formatMoney(value) {
  const number = finite(value)
  return `${number < 0 ? '-' : ''}¥${formatNumber(Math.abs(number), 2)}`
}

export function formatPercent(value) {
  const number = finite(value) * 100
  return `${number >= 0 ? '+' : '-'}${formatNumber(Math.abs(number), 2)}%`
}
