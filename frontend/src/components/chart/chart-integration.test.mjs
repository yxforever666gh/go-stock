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

test('research charts and stock detail reuse MarketChartCanvas with scoped research endpoints', async () => {
  const research = await read('../ResearchTradeChart.vue')
  const stock = await read('../stock.vue')
  assert.match(research, /GetAIRecommendationChart/)
  assert.match(research, /RefreshAIRecommendationChart/)
  assert.match(research, /GetResearch2RecommendationChart/)
  assert.match(research, /RefreshResearch2RecommendationChart/)
  assert.match(research, /scope: \{type: String, default: 'research'/)
  assert.match(research, /MarketChartCanvas/)
  assert.doesNotMatch(research, /GetInstrumentChart|import \* as echarts/)
  assert.match(stock, /ProfessionalKLinePanel/)
  assert.doesNotMatch(stock, /useStockHeavyFeatures/)
})

test('research center 2 recommendations use live valuation, draggable columns and scoped chart detail', async () => {
  const recommendations = await read('../research2Recommendations.vue')
  const yieldPage = await read('../research2Yield.vue')
  const service = await read('../../services/research2-api.ts')
  const contract = await read('../../services/api-types.generated.ts')

  assert.match(recommendations, /useDraggableDataTableColumns/)
  assert.match(recommendations, /go-stock:research2-recommendations:column-order:v1/)
  assert.match(recommendations, /title: '当前价'/)
  assert.doesNotMatch(recommendations, /title: '排名'|title: '目标买入'|title: '操作'|title: '买入区间'|title: '净收益'/)
  assert.match(recommendations, /await GetResearch2Account\(\)[\s\S]*ListResearch2Recommendations/)
  assert.match(recommendations, /ResearchTradeChart scope="research2"/)
  assert.match(yieldPage, /GetResearch2Performance\(\)[\s\S]*ListResearch2Recommendations/)
  assert.match(yieldPage, /ResearchTradeChart scope="research2"/)
  assert.match(yieldPage, /formatDrawdown/)
  assert.match(yieldPage, /yield-positive/)
  assert.match(yieldPage, /yield-negative/)
  assert.match(yieldPage, /yield-table-value/)
  assert.match(service, /API_PATHS\.getResearch2RecommendationChart/)
  assert.match(service, /RefreshResearch2RecommendationChart/)
  assert.match(contract, /getResearch2RecommendationChart: "\/api\/v1\/research2\/recommendations\/\{id\}\/chart"/)
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
