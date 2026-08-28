<script setup>
import {computed, h, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {NButton, NSpace, NTag, NText, useMessage} from 'naive-ui'
import {useRoute, useRouter} from 'vue-router'
import {useMarketDataResource} from '../../composables/useMarketDataResource.js'
import {parseDataEnvelope} from '../../services/data-envelope.js'
import {
  FollowETF,
  GetETFDetail,
  GetETFRankings,
  ListFollowedETFs,
  SearchETFs,
  UnfollowETF,
} from '../../services/fund-market-api'
import EvidenceStatusBar from '../EvidenceStatusBar.vue'
import KLineChart from '../KLineChart.vue'
import {
  ETF_CATEGORIES,
  ETF_SORT_OPTIONS,
  etfIdentity,
  normalizeETFDetail,
  normalizeETFSearchItems,
  normalizeETFRankingPage,
} from './fund-market-model.js'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const active = ref(true)
const category = ref('all')
const sort = ref('changeRate')
const sortDirection = ref('desc')
const queryDraft = ref('')
const query = ref('')
const page = ref(1)
const pageSize = ref(20)
const quickQuery = ref('')
const quickLoading = ref(false)
const quickOptions = ref([])
const followedETFs = ref([])
const followedKeys = ref(new Set())
const favoritesVisible = ref(false)
const favoriteLoadingCode = ref('')
const detailVisible = ref(false)
const detailLoading = ref(false)
const detailEnvelope = ref(parseDataEnvelope({data: {}, status: 'unavailable'}))
const detailError = ref('')
const detailTab = ref('overview')
let detailRequestVersion = 0
let searchRequestVersion = 0
let searchTimer = null

const requestKey = computed(() => ['etf-ranking', category.value, query.value, sort.value, sortDirection.value, page.value, pageSize.value].join('|'))
const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {items: [], total: 0, page: 1, pageSize: 20, category: 'all'},
  intervalMs: 60000,
  requestKey,
  loader: () => GetETFRankings({
    category: category.value,
    q: query.value,
    sort: sort.value,
    sortDirection: sortDirection.value,
    page: page.value,
    pageSize: pageSize.value,
  }),
})

const rankingPage = computed(() => normalizeETFRankingPage(data.value))
const detail = computed(() => normalizeETFDetail(detailEnvelope.value.data))
const drawerWidth = computed(() => typeof window === 'undefined' ? 1080 : Math.min(1180, Math.max(380, window.innerWidth - 36)))
const holdingColumns = [
  {title: '代码', key: 'code', width: 115},
  {title: '名称', key: 'name', minWidth: 180, ellipsis: {tooltip: true}},
  {title: '权重', key: 'weight', width: 105, render: row => percentage(row.weight, false)},
  {title: '持仓日期', key: 'asOf', width: 125, render: row => row.asOf || '--'},
]
const favoriteColumns = [
  {title: '代码', key: 'code', width: 110},
  {title: '名称', key: 'name', minWidth: 200, ellipsis: {tooltip: true}},
  {title: '市场', key: 'market', width: 85},
  {title: '分类', key: 'category', width: 105, render: row => categoryLabel(row.category)},
  {
    title: '操作', key: 'actions', width: 180,
    render: row => h(NSpace, {size: 6}, {default: () => [
      h(NButton, {size: 'small', tertiary: true, type: 'primary', onClick: () => showFavoriteDetail(row)}, {default: () => '详情 / K线'}),
      h(NButton, {
        size: 'small', secondary: true, type: 'warning', loading: favoriteLoadingCode.value === row.code,
        onClick: () => toggleFavorite(row),
      }, {default: () => '移出自选'}),
    ]}),
  },
]

function nullable(value, digits = 3) {
  return value === null || value === undefined ? '--' : Number(value).toFixed(digits)
}

function percentage(value, signed = true) {
  if (value === null || value === undefined) return '--'
  return `${signed && value >= 0 ? '+' : ''}${Number(value).toFixed(2)}%`
}

function compactAmount(value) {
  if (value === null || value === undefined) return '--'
  const number = Number(value)
  if (Math.abs(number) >= 100000000) return `${(number / 100000000).toFixed(2)} 亿`
  if (Math.abs(number) >= 10000) return `${(number / 10000).toFixed(2)} 万`
  return number.toFixed(2)
}

function categoryLabel(value) {
  return ETF_CATEGORIES.find(item => item.value === value)?.label || value || '--'
}

function followed(row) {
  return followedKeys.value.has(etfIdentity(row))
}

const columns = computed(() => [
  {title: '排名', key: 'rank', width: 70, render: row => row.rank ?? '--'},
  {title: '代码', key: 'code', width: 105},
  {title: 'ETF 名称', key: 'name', minWidth: 190, ellipsis: {tooltip: true}},
  {title: '分类', key: 'category', width: 100, render: row => h(NTag, {size: 'small', bordered: false}, {default: () => categoryLabel(row.category)})},
  {title: '最新价', key: 'price', width: 100, render: row => nullable(row.price)},
  {
    title: '涨跌幅', key: 'changeRate', width: 105,
    render: row => h(NText, {type: row.changeRate === null ? 'default' : row.changeRate >= 0 ? 'error' : 'success'}, {default: () => percentage(row.changeRate)}),
  },
  {title: '成交额', key: 'amount', width: 120, render: row => compactAmount(row.amount)},
  {title: '换手率', key: 'turnoverRate', width: 100, render: row => percentage(row.turnoverRate, false)},
  {title: '单位净值', key: 'nav', width: 100, render: row => nullable(row.nav, 4)},
  {
    title: '溢折价', key: 'premiumRate', width: 105,
    render: row => h(NText, {type: row.premiumRate === null ? 'default' : row.premiumRate >= 0 ? 'error' : 'success'}, {default: () => percentage(row.premiumRate)}),
  },
  {title: '规模', key: 'scale', width: 110, render: row => compactAmount(row.scale)},
  {
    title: '净流入', key: 'netInflow', width: 115,
    render: row => h(NText, {type: row.netInflow === null ? 'default' : row.netInflow >= 0 ? 'error' : 'success'}, {default: () => compactAmount(row.netInflow)}),
  },
  {title: '行情时间', key: 'quoteTime', width: 165, render: row => dateTime(row.quoteTime)},
  {
    title: '操作', key: 'actions', width: 185, fixed: 'right',
    render: row => h(NSpace, {size: 6}, {default: () => [
      h(NButton, {size: 'small', tertiary: true, type: 'primary', onClick: () => showETF(row)}, {default: () => '详情 / K线'}),
      h(NButton, {
        size: 'small', secondary: true, type: followed(row) ? 'warning' : 'default',
        loading: favoriteLoadingCode.value === row.code,
        onClick: () => toggleFavorite(row),
      }, {default: () => followed(row) ? '取消自选' : '加入自选'}),
    ]}),
  },
])

function dateTime(value) {
  return value ? String(value).replace('T', ' ').slice(0, 19) : '--'
}

function resetPage() {
  page.value = 1
}

function applySearch() {
  const next = queryDraft.value.trim()
  const unchanged = next === query.value && page.value === 1
  query.value = next
  page.value = 1
  if (unchanged) refresh()
}

async function loadFavorites() {
  try {
    const response = await ListFollowedETFs()
    followedETFs.value = normalizeETFSearchItems(response)
    followedKeys.value = new Set(followedETFs.value.map(etfIdentity).filter(Boolean))
  } catch (reason) {
    message.warning(`ETF 自选读取失败：${reason?.message || String(reason)}`)
  }
}

async function toggleFavorite(row) {
  if (!row?.code || favoriteLoadingCode.value) return
  favoriteLoadingCode.value = row.code
  const identity = etfIdentity(row)
  try {
    if (followed(row)) {
      await UnfollowETF(row.code)
      const next = new Set(followedKeys.value)
      next.delete(identity)
      followedKeys.value = next
      followedETFs.value = followedETFs.value.filter(item => item.code !== row.code)
      message.success('已移出 ETF 自选')
    } else {
      if (!row.name || !row.market || !row.category || row.category === 'all') throw new Error('ETF 名称、市场或分类缺失，暂不能加入自选')
      await FollowETF({code: row.code, name: row.name, market: row.market, category: row.category})
      followedKeys.value = new Set([...followedKeys.value, identity])
      followedETFs.value = [...followedETFs.value.filter(item => item.code !== row.code), normalizeETFDetail(row)]
      message.success('已加入 ETF 自选')
    }
  } catch (reason) {
    message.error(reason?.message || String(reason))
  } finally {
    favoriteLoadingCode.value = ''
  }
}

function updateQuickQuery(value) {
  quickQuery.value = value
  quickOptions.value = []
  if (searchTimer !== null) clearTimeout(searchTimer)
  const keyword = String(value || '').trim()
  const version = ++searchRequestVersion
  if (!keyword) {
    quickLoading.value = false
    return
  }
  quickLoading.value = true
  searchTimer = setTimeout(async () => {
    try {
      const response = await SearchETFs(keyword, 20)
      if (version !== searchRequestVersion) return
      quickOptions.value = normalizeETFSearchItems(response.data).map(item => ({
        label: `${item.name || '未命名 ETF'} [${item.market || '--'} ${item.code}]`,
        value: item.code,
        item,
      }))
    } catch (reason) {
      if (version === searchRequestVersion) message.error(reason?.message || String(reason))
    } finally {
      if (version === searchRequestVersion) quickLoading.value = false
    }
  }, 250)
}

function selectQuickETF(value) {
  const option = quickOptions.value.find(item => item.value === value)
  showETF(option?.item || {code: value})
}

function showETF(row) {
  if (!row?.code) return
  const nextQuery = {...route.query, fundView: 'rankings', rankingView: 'etfs', etfCode: row.code}
  if (String(route.query.etfCode || '') === row.code) loadETFDetail(row.code)
  else router.replace({name: 'fund', query: nextQuery})
}

function showFavoriteDetail(row) {
  favoritesVisible.value = false
  showETF(row)
}

async function loadETFDetail(code) {
  const normalizedCode = String(code || '').trim()
  if (!normalizedCode) return
  const version = ++detailRequestVersion
  detailVisible.value = true
  detailLoading.value = true
  detailError.value = ''
  detailTab.value = 'overview'
  detailEnvelope.value = parseDataEnvelope({data: {code: normalizedCode}, status: 'unavailable'})
  try {
    const response = await GetETFDetail(normalizedCode)
    if (version === detailRequestVersion) detailEnvelope.value = response
  } catch (reason) {
    if (version !== detailRequestVersion) return
    detailError.value = reason?.message || String(reason)
    detailEnvelope.value = parseDataEnvelope({data: {code: normalizedCode}, status: 'unavailable', errors: [detailError.value]})
  } finally {
    if (version === detailRequestVersion) detailLoading.value = false
  }
}

function updateDetailVisible(value) {
  detailVisible.value = value
  if (value) return
  detailRequestVersion++
  const nextQuery = {...route.query}
  delete nextQuery.etfCode
  router.replace({name: 'fund', query: nextQuery})
}

watch(() => route.query.etfCode, code => {
  const value = String(code || '').trim()
  if (value) loadETFDetail(value)
  else detailVisible.value = false
}, {immediate: true})

onMounted(loadFavorites)
onBeforeUnmount(() => {
  detailRequestVersion++
  searchRequestVersion++
  if (searchTimer !== null) clearTimeout(searchTimer)
})
</script>

<template>
  <section>
    <n-flex :wrap="true" align="center" class="ranking-toolbar">
      <n-select v-model:value="category" :options="ETF_CATEGORIES" style="width: 130px" @update:value="resetPage"/>
      <n-select v-model:value="sort" :options="ETF_SORT_OPTIONS" style="width: 130px" @update:value="resetPage"/>
      <n-select v-model:value="sortDirection" :options="[{label:'降序',value:'desc'},{label:'升序',value:'asc'}]" style="width: 100px" @update:value="resetPage"/>
      <n-input v-model:value="queryDraft" clearable placeholder="排行内搜索名称或代码" style="width: 240px" @keyup.enter="applySearch"/>
      <n-button type="primary" @click="applySearch">搜索排行</n-button>
      <n-auto-complete
        :value="quickQuery"
        :options="quickOptions"
        :loading="quickLoading"
        clearable
        placeholder="快速搜索 ETF 并打开详情"
        style="width: 290px"
        @update:value="updateQuickQuery"
        @select="selectQuickETF"
      />
      <n-button secondary type="warning" @click="favoritesVisible = true">ETF 自选（{{ followedETFs.length }}）</n-button>
    </n-flex>
    <n-alert type="info" :bordered="false" class="boundary-alert">场内 ETF 在此作为行情与行业观察信息展示；自选使用独立 ETF 清单。</n-alert>
    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
    <n-data-table
      :columns="columns"
      :data="rankingPage.items"
      :loading="loading && !rankingPage.items.length"
      :row-key="row => `${row.market}:${row.code}`"
      :scroll-x="1860"
      striped
    />
    <n-flex justify="space-between" align="center" class="ranking-pagination">
      <n-text depth="3">共 {{ rankingPage.total }} 只场内 ETF；排行、搜索与详情均由服务端返回</n-text>
      <n-pagination
        v-model:page="page"
        v-model:page-size="pageSize"
        :item-count="rankingPage.total"
        :page-sizes="[20, 50, 100]"
        show-size-picker
        @update:page-size="resetPage"
      />
    </n-flex>
  </section>

  <n-drawer :show="detailVisible" :width="drawerWidth" @update:show="updateDetailVisible">
    <n-drawer-content :title="`${detail.name || 'ETF 详情'} [${detail.market || '--'} ${detail.code || '--'}]`" closable>
      <EvidenceStatusBar :envelope="detailEnvelope" :error="detailError" :loading="detailLoading" @refresh="loadETFDetail(detail.code)"/>
      <n-flex justify="end" class="detail-action">
        <n-button
          secondary
          :type="followed(detail) ? 'warning' : 'primary'"
          :loading="favoriteLoadingCode === detail.code"
          :disabled="!detail.code || !detail.name || !detail.market || !detail.category || detail.category === 'all'"
          @click="toggleFavorite(detail)"
        >{{ followed(detail) ? '取消 ETF 自选' : '加入 ETF 自选' }}</n-button>
      </n-flex>
      <n-spin :show="detailLoading && !detail.name">
        <n-tabs v-model:value="detailTab" type="line" animated display-directive="if">
          <n-tab-pane name="overview" tab="概览">
            <n-descriptions bordered :column="3" size="small">
              <n-descriptions-item label="最新价">{{ nullable(detail.price) }}</n-descriptions-item>
              <n-descriptions-item label="涨跌幅"><n-text :type="detail.changeRate >= 0 ? 'error' : 'success'">{{ percentage(detail.changeRate) }}</n-text></n-descriptions-item>
              <n-descriptions-item label="行情时间">{{ dateTime(detail.quoteTime) }}</n-descriptions-item>
              <n-descriptions-item label="成交额">{{ compactAmount(detail.amount) }}</n-descriptions-item>
              <n-descriptions-item label="换手率">{{ percentage(detail.turnoverRate, false) }}</n-descriptions-item>
              <n-descriptions-item label="资金净流入">{{ compactAmount(detail.netInflow) }}</n-descriptions-item>
              <n-descriptions-item label="单位净值">{{ nullable(detail.nav, 4) }}</n-descriptions-item>
              <n-descriptions-item label="净值日期">{{ detail.navDate || '--' }}</n-descriptions-item>
              <n-descriptions-item label="溢折价"><n-text :type="detail.premiumRate >= 0 ? 'error' : 'success'">{{ percentage(detail.premiumRate) }}</n-text></n-descriptions-item>
              <n-descriptions-item label="基金份额">{{ compactAmount(detail.shares) }}</n-descriptions-item>
              <n-descriptions-item label="基金规模">{{ compactAmount(detail.scale) }}</n-descriptions-item>
              <n-descriptions-item label="分类">{{ categoryLabel(detail.category) }}</n-descriptions-item>
              <n-descriptions-item label="跟踪指数">{{ detail.trackingIndex || '--' }}</n-descriptions-item>
              <n-descriptions-item label="管理费">{{ percentage(detail.managementFee, false) }}</n-descriptions-item>
              <n-descriptions-item label="上市日期">{{ detail.listDate || '--' }}</n-descriptions-item>
            </n-descriptions>
          </n-tab-pane>
          <n-tab-pane name="chart" tab="专业 K 线">
            <KLineChart
              v-if="detail.chartInstrument.code"
              :code="detail.chartInstrument.code"
              :stock-name="detail.name"
              asset-type="etf"
              :market="detail.chartInstrument.market"
              period="day"
              adjustment="none"
              :chart-height="560"
            />
            <n-empty v-else description="暂无可用图表证券标识"/>
          </n-tab-pane>
          <n-tab-pane name="holdings" tab="主要持仓">
            <n-data-table
              :columns="holdingColumns"
              :data="detail.holdings"
              :row-key="row => row._key"
              :scroll-x="560"
              striped
            />
            <n-empty v-if="!detail.holdings.length" description="暂无主要持仓数据"/>
          </n-tab-pane>
        </n-tabs>
      </n-spin>
    </n-drawer-content>
  </n-drawer>

  <n-modal v-model:show="favoritesVisible">
    <n-card title="ETF 自选清单" closable style="width:min(820px,94vw);max-height:88vh" @close="favoritesVisible=false">
      <n-data-table
        :columns="favoriteColumns"
        :data="followedETFs"
        :row-key="row => `${row.market}:${row.code}`"
        :scroll-x="700"
        :max-height="560"
        striped
      />
      <n-empty v-if="!followedETFs.length" description="暂无 ETF 自选"/>
    </n-card>
  </n-modal>
</template>

<style scoped>
.ranking-toolbar,
.ranking-pagination,
.detail-action {
  gap: 10px;
  margin: 10px 0;
}

.boundary-alert {
  margin-bottom: 10px;
}
</style>
