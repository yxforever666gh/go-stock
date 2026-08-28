<script setup>
import {computed, ref, toRef, watch} from 'vue'
import EvidenceStatusBar from '../components/EvidenceStatusBar.vue'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import {GetMarketMargin} from '../services/market-api.js'
import {dateValue, firstValue, itemCode, itemName, latestDatedRows, numberValue, rowsFrom} from './market-data.js'

const props = defineProps({active: {type: Boolean, default: false}})
const active = toRef(props, 'active')
const scope = ref('market')
const codeInput = ref('')
const code = ref('')
const selectedDate = ref(null)
const requestCode = computed(() => scope.value === 'security' ? code.value : '')
const requestKey = computed(() => ['margin', scope.value, requestCode.value, selectedDate.value || ''].join('|'))

const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {rows: []},
  intervalMs: 300000,
  loader: () => scope.value === 'security' && !requestCode.value
    ? Promise.resolve({data: {rows: []}, status: 'unavailable', errors: ['请输入股票代码']})
    : GetMarketMargin({scope: scope.value, code: requestCode.value, date: selectedDate.value}),
  requestKey,
})

const rawRows = computed(() => {
  const collection = rowsFrom(data.value)
  if (collection.length) return collection
  if (data.value && typeof data.value === 'object' && ['marginBalance', 'financingBalance', 'securitiesBalance'].some(key => key in data.value)) return [data.value]
  return []
})
const rows = computed(() => rawRows.value.map((row, index) => ({
  ...row,
  _key: itemCode(row, index),
  _name: itemName(row),
  _date: dateValue(row).slice(0, 10),
  _financingBalance: numberValue(row, ['financingBalance', 'rzye', 'financing_balance']),
  _securitiesBalance: numberValue(row, ['securitiesBalance', 'rqye', 'securities_balance']),
  _totalBalance: numberValue(row, ['totalBalance', 'lrye', 'marginBalance', 'total_balance']),
  _financingBuy: numberValue(row, ['financingBuy', 'financing_buy', 'rzmre']),
  _securitiesSell: numberValue(row, ['securitiesSell', 'securities_sell', 'rqmcl']),
})))
const latestDate = computed(() => latestDatedRows(rows.value)[0]?._date || '')
const summaryRows = computed(() => latestDatedRows(rows.value, {single: scope.value === 'security'}))

const summary = computed(() => {
  const explicit = data.value?.summary
  if (explicit) return explicit
  return {
    financingBalance: summaryRows.value.reduce((total, row) => total + row._financingBalance, 0),
    securitiesBalance: summaryRows.value.reduce((total, row) => total + row._securitiesBalance, 0),
    marginBalance: summaryRows.value.reduce((total, row) => total + row._totalBalance, 0),
    financingBuy: summaryRows.value.reduce((total, row) => total + row._financingBuy, 0),
    securitiesSell: summaryRows.value.reduce((total, row) => total + row._securitiesSell, 0),
  }
})

function formatAmount(value) {
  const number = Number(value)
  if (!Number.isFinite(number)) return '--'
  if (Math.abs(number) >= 100000000) return `${(number / 100000000).toFixed(2)} 亿`
  if (Math.abs(number) >= 10000) return `${(number / 10000).toFixed(2)} 万`
  return number.toFixed(2)
}

const columns = [
  {title: '日期', key: '_date', width: 110},
  {title: '代码', key: '_key', width: 110},
  {title: '名称', key: '_name', minWidth: 130, ellipsis: {tooltip: true}},
  {title: '融资余额', key: '_financingBalance', width: 130, sorter: (a, b) => a._financingBalance - b._financingBalance, render: row => formatAmount(row._financingBalance)},
  {title: '融券余额', key: '_securitiesBalance', width: 130, sorter: (a, b) => a._securitiesBalance - b._securitiesBalance, render: row => formatAmount(row._securitiesBalance)},
  {title: '两融余额', key: '_totalBalance', width: 130, sorter: (a, b) => a._totalBalance - b._totalBalance, render: row => formatAmount(row._totalBalance)},
  {title: '融资买入', key: '_financingBuy', width: 130, sorter: (a, b) => a._financingBuy - b._financingBuy, render: row => formatAmount(row._financingBuy)},
  {title: '融券卖出', key: '_securitiesSell', width: 130, sorter: (a, b) => a._securitiesSell - b._securitiesSell, render: row => formatAmount(row._securitiesSell)},
]

function applyCode() {
  const nextCode = codeInput.value.trim()
  if (scope.value === 'security' && !nextCode) {
    code.value = ''
    return
  }
  if (nextCode === code.value) void refresh()
  else code.value = nextCode
}

watch(scope, next => {
  if (next === 'market') code.value = ''
})
</script>

<template>
  <section>
    <n-flex align="center" :wrap="true" class="margin-toolbar">
      <n-select v-model:value="scope" :options="[{label:'市场总览',value:'market'},{label:'个股明细',value:'security'}]" style="width: 130px"/>
      <n-input-group v-if="scope === 'security'" style="width: 260px">
        <n-input v-model:value="codeInput" placeholder="股票代码" clearable @keyup.enter="applyCode"/>
        <n-button type="primary" @click="applyCode">查询</n-button>
      </n-input-group>
      <n-date-picker v-model:formatted-value="selectedDate" type="date" value-format="yyyy-MM-dd" clearable :is-date-disabled="ts => ts > Date.now()" style="width: 150px"/>
      <n-text depth="3">数据日：{{ latestDate || '--' }}</n-text>
    </n-flex>
    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
    <n-grid :cols="5" :x-gap="12" class="margin-summary">
      <n-gi><n-statistic label="融资余额" :value="formatAmount(firstValue(summary, ['financingBalance', 'rzye'], 0))"/></n-gi>
      <n-gi><n-statistic label="融券余额" :value="formatAmount(firstValue(summary, ['securitiesBalance', 'rqye'], 0))"/></n-gi>
      <n-gi><n-statistic label="两融余额" :value="formatAmount(firstValue(summary, ['marginBalance', 'totalBalance', 'lrye'], 0))"/></n-gi>
      <n-gi><n-statistic label="融资买入" :value="formatAmount(firstValue(summary, ['financingBuy', 'rzmre'], 0))"/></n-gi>
      <n-gi><n-statistic label="融券卖出" :value="formatAmount(firstValue(summary, ['securitiesSell', 'rqmcl'], 0))"/></n-gi>
    </n-grid>
    <n-data-table
      :columns="columns"
      :data="rows"
      :loading="loading && !rows.length"
      :row-key="row => `${row._key}-${row._date}`"
      :scroll-x="980"
      :max-height="620"
      striped
    />
    <n-empty v-if="!loading && !rows.length" description="暂无融资融券数据"/>
  </section>
</template>

<style scoped>
.margin-toolbar,
.margin-summary {
  margin-bottom: 10px;
}
</style>
