<script setup>
import {defineAsyncComponent, onBeforeMount, onBeforeUnmount, ref, watch} from 'vue'
import { GetConfig } from '../services/settings-api'
import { GetIndustryRank, GetTelegraphList, GlobalStockIndexes, ReFleshTelegraphList } from '../services/market-api'
import { EventsOff, EventsOn } from '../services/browser-runtime.mjs'
import {useRoute, useRouter} from 'vue-router'
import {DEFAULT_MARKET_TAB, findMarketTab, MARKET_TABS} from '../market-tabs/market-tab-registry.js'
import {createPollingController} from '../composables/usePolling.js'
import {isChinaTradingSession} from '../market-tabs/market-session.js'

const route = useRoute()
const router = useRouter()
const panelHeight = ref(window.innerHeight - 240)
const darkTheme = ref(false)
const telegraphList = ref([])
const sinaNewsList = ref([])
const foreignNewsList = ref([])
const globalStockIndexes = ref({})
const industryRanks = ref([])
const sort = ref('0')
const initialTab = findMarketTab(String(route.query.name || ''))?.name || DEFAULT_MARKET_TAB
const nowTab = ref(initialTab)
const stockCode = ref('')
const marketTabs = MARKET_TABS.map(tab => ({...tab, component: defineAsyncComponent(tab.load)}))

function updateTab(name, syncRoute = true) {
  if (!findMarketTab(name)) return
  nowTab.value = name
  if (syncRoute && route.query.name !== name) {
    void router.replace({name: 'market', query: {...route.query, name}})
  }
}

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

function hasOpenGlobalMarket() {
  const states = Object.values(globalStockIndexes.value || {}).flatMap(value => Array.isArray(value) ? value : [])
    .map(item => String(item?.state || '').toLowerCase()).filter(Boolean)
  return states.length === 0 || states.includes('open')
}

const newsPolling = createPollingController(
  () => Promise.all([refreshNews('财联社电报'), refreshNews('新浪财经'), refreshNews('外媒')]),
  10000,
  {shouldRun: () => nowTab.value === '市场快讯' && isChinaTradingSession()},
)
const indexPolling = createPollingController(refreshIndexes, 3000, {
  shouldRun: () => nowTab.value === '全球股指' && hasOpenGlobalMarket(),
})
const industryPolling = createPollingController(refreshIndustry, 10000, {
  shouldRun: () => nowTab.value === '行业排名' && isChinaTradingSession(),
})

async function activateLegacyTab(name) {
  newsPolling.stop()
  indexPolling.stop()
  industryPolling.stop()
  if (name === '市场快讯') {
    const [cls, sina, foreign] = await Promise.all([GetTelegraphList('财联社电报'), GetTelegraphList('新浪财经'), GetTelegraphList('外媒')])
    telegraphList.value = cls || []
    sinaNewsList.value = sina || []
    foreignNewsList.value = foreign || []
    newsPolling.start({immediate: false})
  } else if (name === '全球股指') {
    await refreshIndexes()
    indexPolling.start({immediate: false})
  } else if (name === '行业排名') {
    await refreshIndustry()
    industryPolling.start({immediate: false})
  }
}

watch(nowTab, name => { void activateLegacyTab(name) })
watch(() => route.query.name, name => updateTab(findMarketTab(String(name || ''))?.name || DEFAULT_MARKET_TAB, false))
watch(() => route.query.stockCode, code => { stockCode.value = String(code || '') })

onBeforeMount(async () => {
  stockCode.value = String(route.query.stockCode || '')
  window.onresize = () => { panelHeight.value = window.innerHeight - 240 }
  const cfg = await GetConfig(); darkTheme.value = cfg?.darkTheme === true
  for (const event of ['changeMarketTab','newTelegraph','newSinaNews','tradingViewNews']) EventsOff(event)
  EventsOn('changeMarketTab', (msg) => updateTab(msg.name))
  EventsOn('newTelegraph', (rows) => { if (rows) telegraphList.value = [...rows, ...telegraphList.value].slice(0, telegraphList.value.length || rows.length) })
  EventsOn('newSinaNews', (rows) => { if (rows) sinaNewsList.value = [...rows, ...sinaNewsList.value].slice(0, sinaNewsList.value.length || rows.length) })
  EventsOn('tradingViewNews', (rows) => { if (rows) foreignNewsList.value = [...rows, ...foreignNewsList.value].slice(0, foreignNewsList.value.length || rows.length) })
  await activateLegacyTab(nowTab.value)
})

onBeforeUnmount(() => {
  for (const event of ['changeMarketTab','newTelegraph','newSinaNews','tradingViewNews']) EventsOff(event)
  newsPolling.stop(); indexPolling.stop(); industryPolling.stop(); window.onresize = null
})
</script>

<template>
  <n-card>
    <n-tabs type="line" animated @update-value="updateTab" :value="nowTab">
      <n-tab-pane v-for="tab in marketTabs" :key="tab.key" :name="tab.name" :tab="tab.name">
        <component
          :is="tab.component"
          v-if="nowTab === tab.name"
          v-bind="tab.activeAware ? {active: nowTab === tab.name} : {}"
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
