import {ref, watch} from 'vue'

import {
  readPriceLinesPreference,
  RESEARCH_CHART_PRICE_LINES_STORAGE_KEY,
  writePriceLinesPreference,
} from '../utils/research-trade-chart'

function browserStorage() {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage
  } catch (_) {
    return null
  }
}

const storage = browserStorage()
const showPriceLines = ref(readPriceLinesPreference(storage))

watch(showPriceLines, value => writePriceLinesPreference(storage, value))

if (typeof window !== 'undefined') {
  window.addEventListener('storage', event => {
    if (event.key !== RESEARCH_CHART_PRICE_LINES_STORAGE_KEY) return
    showPriceLines.value = event.newValue === 'true'
  })
}

export function useResearchChartPreferences() {
  return {showPriceLines}
}
