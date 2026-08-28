// Compatibility adapter for extensions that still import the historical composable.
// Rendering now belongs exclusively to ProfessionalKLinePanel/MarketChartCanvas.
export function useStockHeavyFeatures({data} = {}) {
  function renderMinuteChart(code, name) {
    if (data) {
      data.code = code
      data.name = name
    }
    return {code, name, assetType: 'stock', period: '1m', adjustment: 'none', viewMode: 'line'}
  }

  function renderDailyKLine() {
    return {
      code: data?.code || '',
      name: data?.name || '',
      assetType: 'stock',
      period: 'day',
      adjustment: 'qfq',
      viewMode: 'candle',
    }
  }

  return {renderDailyKLine, renderMinuteChart}
}
