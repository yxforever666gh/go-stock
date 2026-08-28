import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import test from 'node:test'

const read = path => readFile(new URL(path, import.meta.url), 'utf8')

test('legacy KLine facade keeps public props and delegates to the professional panel', async () => {
  const source = await read('../KLineChart.vue')
  for (const prop of ['code', 'stockName', 'kDays', 'chartHeight', 'darkTheme']) assert.match(source, new RegExp(`${prop}:`))
  assert.match(source, /ProfessionalKLinePanel/)
  assert.doesNotMatch(source, /echarts|GetStockKLine/)
})

test('research chart and stock detail reuse MarketChartCanvas without changing research endpoint', async () => {
  const research = await read('../ResearchTradeChart.vue')
  const stock = await read('../stock.vue')
  assert.match(research, /GetAIRecommendationChart/)
  assert.match(research, /RefreshAIRecommendationChart/)
  assert.match(research, /MarketChartCanvas/)
  assert.doesNotMatch(research, /GetInstrumentChart|import \* as echarts/)
  assert.match(stock, /ProfessionalKLinePanel/)
  assert.doesNotMatch(stock, /useStockHeavyFeatures/)
})

test('microstructure drawer exposes auction, intraday and trades while source metadata reads provider', async () => {
  const drawer = await read('../InstrumentMicrostructureDrawer.vue')
  const meta = await read('./ChartDataMeta.vue')
  assert.match(drawer, /name="auction"/)
  assert.match(drawer, /name="intraday"/)
  assert.match(drawer, /name="trades"/)
  assert.match(meta, /value\?\.provider/)
})

test('professional panel keeps HK and US out of new drawings and microstructure APIs', async () => {
  const panel = await read('./ProfessionalKLinePanel.vue')
  const globalIndexes = await read('../../market-tabs/GlobalIndexesTab.vue')
  const majorIndexes = await read('../../market-tabs/MajorIndexesTab.vue')
  assert.match(panel, /isLegacyChartInstrument/)
  assert.match(panel, /legacy: legacy\.value/)
  assert.match(panel, /legacy \? \[\] : drawingStore/)
  assert.match(panel, /showMicrostructure && !legacy/)
  assert.match(panel, /legacy 兼容数据源/)
  assert.match(globalIndexes, /code="hkHSI" asset-type="index" market="HK"/)
  assert.match(globalIndexes, /code="us\.IXIC" asset-type="index" market="US"/)
  assert.match(majorIndexes, /code="usYINN\.AM" asset-type="etf" market="US"/)
})
