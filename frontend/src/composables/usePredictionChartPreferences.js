import {ref, watch} from 'vue'

import {
  readPriceLinesPreference,
  PREDICTION_CHART_PRICE_LINES_STORAGE_KEY,
  writePriceLinesPreference,
} from '../utils/prediction-trade-chart'

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
    if (event.key !== PREDICTION_CHART_PRICE_LINES_STORAGE_KEY) return
    showPriceLines.value = event.newValue === 'true'
  })
}

export function usePredictionChartPreferences() {
  return {showPriceLines}
}
