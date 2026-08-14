<script setup>
import { defineAsyncComponent, onBeforeMount, onBeforeUnmount, ref } from 'vue'
import { GetConfig, GetIndustryRank, GetTelegraphList, GlobalStockIndexes, ReFleshTelegraphList } from '../services/app-api'
import { EventsOff, EventsOn } from '../services/browser-runtime.mjs'
import { useRoute } from 'vue-router'

const MarketNewsTab = defineAsyncComponent(() => import('../market-tabs/MarketNewsTab.vue'))
const GlobalIndexesTab = defineAsyncComponent(() => import('../market-tabs/GlobalIndexesTab.vue'))
const MajorIndexesTab = defineAsyncComponent(() => import('../market-tabs/MajorIndexesTab.vue'))
const IndustryRankTab = defineAsyncComponent(() => import('../market-tabs/IndustryRankTab.vue'))
const MoneyFlowTab = defineAsyncComponent(() => import('../market-tabs/MoneyFlowTab.vue'))
const LongTigerTab = defineAsyncComponent(() => import('../market-tabs/LongTigerTab.vue'))
const StockResearchTab = defineAsyncComponent(() => import('../market-tabs/StockResearchTab.vue'))
const StockNoticeTab = defineAsyncComponent(() => import('../market-tabs/StockNoticeTab.vue'))
const IndustryResearchTab = defineAsyncComponent(() => import('../market-tabs/IndustryResearchTab.vue'))
const HotTopicsTab = defineAsyncComponent(() => import('../market-tabs/HotTopicsTab.vue'))
const SelectStockTab = defineAsyncComponent(() => import('../market-tabs/SelectStockTab.vue'))
const FavoriteSitesTab = defineAsyncComponent(() => import('../market-tabs/FavoriteSitesTab.vue'))

const route = useRoute()
const panelHeight = ref(window.innerHeight - 240)
const darkTheme = ref(false)
const telegraphList = ref([])
const sinaNewsList = ref([])
const foreignNewsList = ref([])
const globalStockIndexes = ref({})
const industryRanks = ref([])
const sort = ref('0')
const nowTab = ref('市场快讯')
const stockCode = ref('')
const visitedTabs = ref([])
let indexTimer = null
let marketTimer = null

const marketTabComponents = {
  市场快讯: MarketNewsTab,
  全球股指: GlobalIndexesTab,
  重大指数: MajorIndexesTab,
  行业排名: IndustryRankTab,
  个股资金流向: MoneyFlowTab,
  龙虎榜: LongTigerTab,
  个股研报: StockResearchTab,
  公司公告: StockNoticeTab,
  行业研究: IndustryResearchTab,
  当前热门: HotTopicsTab,
  指标选股: SelectStockTab,
  名站优选: FavoriteSitesTab,
}

function markVisited(name) {
  if (name && !visitedTabs.value.includes(name)) visitedTabs.value = [...visitedTabs.value, name]
}
function updateTab(name) { nowTab.value = name; markVisited(name) }
function shouldRenderTab(name) { return visitedTabs.value.includes(name) }

async function refreshNews(source) {
  const rows = await ReFleshTelegraphList(source)
  if (source === '财联社电报') telegraphList.value = rows || []
  if (source === '新浪财经') sinaNewsList.value = rows || []
  if (source === '外媒') foreignNewsList.value = rows || []
}
async function refreshIndexes() {
  try { globalStockIndexes.value = await GlobalStockIndexes() || {} } catch { globalStockIndexes.value = {} }
}
async function refreshIndustry() {
  try { industryRanks.value = await GetIndustryRank(sort.value, 150) || [] } catch { industryRanks.value = [] }
}
function changeIndustryRankSort() { sort.value = sort.value === '0' ? '1' : '0'; refreshIndustry() }

onBeforeMount(async () => {
  const initial = String(route.query.name || '市场快讯')
  nowTab.value = initial; stockCode.value = String(route.query.stockCode || ''); markVisited(initial)
  window.onresize = () => { panelHeight.value = window.innerHeight - 240 }
  const cfg = await GetConfig(); darkTheme.value = cfg?.darkTheme === true
  const [cls, sina, foreign] = await Promise.all([GetTelegraphList('财联社电报'), GetTelegraphList('新浪财经'), GetTelegraphList('外媒')])
  telegraphList.value = cls || []; sinaNewsList.value = sina || []; foreignNewsList.value = foreign || []
  await Promise.all([refreshIndexes(), refreshIndustry()])
  indexTimer = setInterval(refreshIndexes, 3000)
  marketTimer = setInterval(() => { refreshIndustry(); refreshNews('财联社电报'); refreshNews('新浪财经'); refreshNews('外媒') }, 10000)
  for (const event of ['changeMarketTab','newTelegraph','newSinaNews','tradingViewNews']) EventsOff(event)
  EventsOn('changeMarketTab', (msg) => updateTab(msg.name))
  EventsOn('newTelegraph', (rows) => { if (rows) telegraphList.value = [...rows, ...telegraphList.value].slice(0, telegraphList.value.length || rows.length) })
  EventsOn('newSinaNews', (rows) => { if (rows) sinaNewsList.value = [...rows, ...sinaNewsList.value].slice(0, sinaNewsList.value.length || rows.length) })
  EventsOn('tradingViewNews', (rows) => { if (rows) foreignNewsList.value = [...rows, ...foreignNewsList.value].slice(0, foreignNewsList.value.length || rows.length) })
})

onBeforeUnmount(() => {
  for (const event of ['changeMarketTab','newTelegraph','newSinaNews','tradingViewNews']) EventsOff(event)
  clearInterval(indexTimer); clearInterval(marketTimer); window.onresize = null
})
</script>

<template>
  <n-card>
    <n-tabs type="line" animated @update-value="updateTab" :value="nowTab" style="--wails-draggable:no-drag">
      <n-tab-pane v-for="(component, name) in marketTabComponents" :key="name" :name="name" :tab="name">
        <component
          :is="component"
          v-if="shouldRenderTab(name)"
          :dark-theme="darkTheme"
          :telegraph-list="telegraphList"
          :sina-news-list="sinaNewsList"
          :foreign-news-list="foreignNewsList"
          :global-stock-indexes="globalStockIndexes"
          :panel-height="panelHeight"
          :industry-ranks="industryRanks"
          :sort="sort"
          :stock-code="stockCode"
          @refresh="refreshNews"
          @toggle-sort="changeIndustryRankSort"
        />
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>
