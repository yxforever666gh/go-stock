function tradingDate(value) {
  return String(value || '').slice(0, 10)
}

export const RESEARCH_CHART_PRICE_LINES_STORAGE_KEY = 'go-stock.research-chart.show-price-lines'

export function readPriceLinesPreference(storage) {
  try {
    return storage?.getItem(RESEARCH_CHART_PRICE_LINES_STORAGE_KEY) === 'true'
  } catch (_) {
    return false
  }
}

export function writePriceLinesPreference(storage, value) {
  try {
    storage?.setItem(RESEARCH_CHART_PRICE_LINES_STORAGE_KEY, value ? 'true' : 'false')
  } catch (_) {
    // Browser storage can be unavailable in privacy-restricted environments.
  }
}

export function weightedExecutionPrice(trades, side) {
  const expectedSide = String(side || '').trim().toLowerCase()
  const matchingTrades = (trades || []).filter(item => {
    const quantity = Number(item?.quantity)
    const price = Number(item?.executionPrice)
    return String(item?.side || '').trim().toLowerCase() === expectedSide
      && Number.isFinite(quantity) && quantity > 0
      && Number.isFinite(price) && price > 0
  })
  const quantity = matchingTrades.reduce((sum, item) => sum + Number(item.quantity), 0)
  if (quantity <= 0) return 0
  return matchingTrades.reduce((sum, item) => sum + Number(item.executionPrice) * Number(item.quantity), 0) / quantity
}

export function tradingDaySeparatorIndexes(categories) {
  const separators = new Set()
  for (let index = 1; index < categories.length; index += 1) {
    const currentDate = tradingDate(categories[index])
    const previousDate = tradingDate(categories[index - 1])
    if (currentDate && previousDate && currentDate !== previousDate) {
      separators.add(index)
    }
  }
  return separators
}
