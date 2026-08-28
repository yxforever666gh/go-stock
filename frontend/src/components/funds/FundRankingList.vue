<script setup>
import {computed, h, ref} from 'vue'
import {NTag, NText} from 'naive-ui'
import {useMarketDataResource} from '../../composables/useMarketDataResource.js'
import {GetFundRankings} from '../../services/fund-market-api'
import EvidenceStatusBar from '../EvidenceStatusBar.vue'
import {
  FUND_CATEGORIES,
  FUND_PERIODS,
  fundPeriodMetric,
  normalizeFundRankingPage,
} from './fund-market-model.js'

const active = ref(true)
const category = ref('all')
const period = ref('day')
const sortDirection = ref('desc')
const queryDraft = ref('')
const query = ref('')
const page = ref(1)
const pageSize = ref(20)
const requestKey = computed(() => ['fund-ranking', category.value, period.value, query.value, sortDirection.value, page.value, pageSize.value].join('|'))
const selectedPeriodLabel = computed(() => FUND_PERIODS.find(item => item.value === period.value)?.label || period.value)

const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {items: [], total: 0, page: 1, pageSize: 20, category: 'all', period: 'day', navDate: ''},
  intervalMs: 300000,
  session: 'always',
  requestKey,
  loader: () => GetFundRankings({
    category: category.value,
    period: period.value,
    q: query.value,
    sortDirection: sortDirection.value,
    page: page.value,
    pageSize: pageSize.value,
  }),
})

const rankingPage = computed(() => normalizeFundRankingPage(data.value))

function nullable(value, digits = 2) {
  return value === null || value === undefined ? '--' : Number(value).toFixed(digits)
}

function percent(value) {
  if (value === null || value === undefined) return '--'
  return `${value >= 0 ? '+' : ''}${Number(value).toFixed(2)}%`
}

function amount(value) {
  if (value === null || value === undefined) return '--'
  const number = Number(value)
  if (Math.abs(number) >= 100000000) return `${(number / 100000000).toFixed(2)} 亿`
  if (Math.abs(number) >= 10000) return `${(number / 10000).toFixed(2)} 万`
  return number.toFixed(2)
}

function categoryLabel(value) {
  return FUND_CATEGORIES.find(item => item.value === value)?.label || value || '--'
}

const columns = computed(() => {
  const metricTitle = period.value === 'scale' ? '基金规模' : `${selectedPeriodLabel.value}收益`
  return [
    {title: '排名', key: 'rank', width: 74, render: row => row.rank ?? '--'},
    {title: '代码', key: 'code', width: 105},
    {title: '基金名称', key: 'name', minWidth: 220, ellipsis: {tooltip: true}},
    {title: '类型', key: 'category', width: 105, render: row => h(NTag, {size: 'small', bordered: false}, {default: () => categoryLabel(row.category)})},
    {title: '单位净值', key: 'nav', width: 110, render: row => nullable(row.nav, 4)},
    {title: '净值日期', key: 'navDate', width: 115, render: row => row.navDate || rankingPage.value.navDate || '--'},
    {
      title: metricTitle,
      key: 'metric',
      width: 125,
      render: row => {
        const value = fundPeriodMetric(row, period.value)
        if (period.value === 'scale') return amount(value)
        return h(NText, {type: value === null ? 'default' : value >= 0 ? 'error' : 'success'}, {default: () => percent(value)})
      },
    },
    {title: '规模日期', key: 'scaleDate', width: 115, render: row => row.scaleDate || '--'},
  ]
})

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
</script>

<template>
  <section>
    <n-flex :wrap="true" align="center" class="ranking-toolbar">
      <n-select v-model:value="category" :options="FUND_CATEGORIES" style="width: 130px" @update:value="resetPage"/>
      <n-select v-model:value="period" :options="FUND_PERIODS" style="width: 135px" @update:value="resetPage"/>
      <n-select v-model:value="sortDirection" :options="[{label:'降序',value:'desc'},{label:'升序',value:'asc'}]" style="width: 100px" @update:value="resetPage"/>
      <n-input v-model:value="queryDraft" clearable placeholder="基金名称或代码" style="width: 240px" @keyup.enter="applySearch"/>
      <n-button type="primary" @click="applySearch">搜索</n-button>
      <n-text depth="3">服务端分页与排序；规模排序时收益列显示基金规模</n-text>
    </n-flex>
    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
    <n-data-table
      :columns="columns"
      :data="rankingPage.items"
      :loading="loading && !rankingPage.items.length"
      :row-key="row => row.code"
      :scroll-x="980"
      striped
    />
    <n-flex justify="space-between" align="center" class="ranking-pagination">
      <n-text depth="3">共 {{ rankingPage.total }} 只；数据净值日 {{ rankingPage.navDate || '--' }}</n-text>
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
</template>

<style scoped>
.ranking-toolbar,
.ranking-pagination {
  gap: 10px;
  margin: 10px 0;
}
</style>
