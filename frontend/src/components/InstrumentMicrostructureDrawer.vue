<script setup>
import {computed, ref, watch} from 'vue'
import EvidenceStatusBar from './EvidenceStatusBar.vue'
import {markEnvelopeStale, parseDataEnvelope} from '../services/data-envelope.js'
import {GetInstrumentAuction, GetInstrumentTrades} from '../services/market-api.js'
import {auctionSummaryFrom, dateValue, firstValue, formatOptionalMetric, numberValue, rowsFrom} from '../market-tabs/market-data.js'
import {shanghaiDate} from '../market-tabs/market-session.js'
import {hasUsableEnvelopeData} from '../composables/useMarketDataResource.js'

const props = defineProps({
  show: {type: Boolean, default: false},
  code: {type: String, default: ''},
  name: {type: String, default: ''},
  assetType: {type: String, default: 'stock'},
  date: {type: String, default: ''},
  defaultTab: {type: String, default: 'auction'},
})
const emit = defineEmits(['update:show'])

const visible = computed({get: () => props.show, set: value => emit('update:show', value)})
const activeTab = ref(props.defaultTab)
const auctionEnvelope = ref(emptyAuctionEnvelope())
const tradesEnvelope = ref(emptyTradesEnvelope())
const auctionLoading = ref(false)
const tradesLoading = ref(false)
const auctionError = ref('')
const tradesError = ref('')
const tradeRows = ref([])
const nextCursor = ref('')
let auctionSucceeded = false
let tradesSucceeded = false
let requestVersion = 0

const requestDate = computed(() => props.date || shanghaiDate())
const instrumentIdentity = computed(() => [props.code.trim(), props.assetType, requestDate.value].join('|'))
const auctionRows = computed(() => rowsFrom(auctionEnvelope.value.data))
const auctionSummary = computed(() => auctionSummaryFrom(auctionEnvelope.value.data))
const auctionData = computed(() => auctionEnvelope.value.data && !Array.isArray(auctionEnvelope.value.data) ? auctionEnvelope.value.data : {})

function formatOptionalNumber(row, keys) {
  return formatOptionalMetric(row, keys)
}

function emptyAuctionEnvelope() {
  return parseDataEnvelope({data: {snapshots: []}, status: 'unavailable'})
}

function emptyTradesEnvelope() {
  return parseDataEnvelope({data: {items: []}, status: 'unavailable'})
}

function resetInstrumentState() {
  requestVersion += 1
  auctionEnvelope.value = emptyAuctionEnvelope()
  tradesEnvelope.value = emptyTradesEnvelope()
  auctionLoading.value = false
  tradesLoading.value = false
  auctionError.value = ''
  tradesError.value = ''
  tradeRows.value = []
  nextCursor.value = ''
  auctionSucceeded = false
  tradesSucceeded = false
}

const auctionColumns = [
  {title: '时间', key: 'at', width: 150, render: row => dateValue(row).replace('T', ' ').slice(0, 19)},
  {title: '指示价格', key: 'price', width: 110, render: row => numberValue(row, ['price', 'indicativePrice', 'matchPrice']).toFixed(3)},
  {title: '匹配量', key: 'matchedVolume', width: 120, render: row => numberValue(row, ['matchedVolume', 'matchVolume', 'volume']).toLocaleString()},
  {title: '未匹配量', key: 'unmatchedVolume', width: 120, render: row => formatOptionalNumber(row, ['unmatchedVolume', 'unmatchVolume'])},
  {title: '未匹配方向', key: 'side', width: 110, render: row => firstValue(row, ['unmatchedSide', 'side', 'direction'], '--')},
]
const tradeColumns = [
  {title: '时间', key: 'at', width: 150, render: row => dateValue(row).replace('T', ' ').slice(0, 19)},
  {title: '价格', key: 'price', width: 100, render: row => numberValue(row, ['price', 'tradePrice']).toFixed(3)},
  {title: '成交量', key: 'volume', width: 120, render: row => numberValue(row, ['volume', 'tradeVolume']).toLocaleString()},
  {title: '成交额', key: 'amount', width: 130, render: row => numberValue(row, ['amount', 'tradeAmount']).toLocaleString()},
  {title: '方向', key: 'side', width: 90, render: row => firstValue(row, ['side', 'direction'], '--')},
]

async function loadAuction() {
  if (!props.code) return
  const version = ++requestVersion
  auctionLoading.value = true
  try {
    const result = await GetInstrumentAuction(props.code, {assetType: props.assetType, date: requestDate.value})
    if (version !== requestVersion) return
    if (hasUsableEnvelopeData(result)) {
      auctionEnvelope.value = result
      auctionError.value = ''
      auctionSucceeded = true
    } else if (auctionSucceeded) {
      auctionEnvelope.value = markEnvelopeStale(auctionEnvelope.value, result.errors?.[0] || `数据状态：${result.status}`)
    } else {
      auctionEnvelope.value = result
    }
  } catch (reason) {
    if (version !== requestVersion) return
    auctionError.value = reason?.message || String(reason)
    auctionEnvelope.value = auctionSucceeded
      ? markEnvelopeStale(auctionEnvelope.value, reason)
      : {...markEnvelopeStale({data: {rows: []}}, reason), status: 'unavailable', stale: false}
  } finally {
    if (version === requestVersion) auctionLoading.value = false
  }
}

async function loadTrades({append = false} = {}) {
  if (!props.code || (append && !nextCursor.value)) return
  const version = ++requestVersion
  tradesLoading.value = true
  try {
    const result = await GetInstrumentTrades(props.code, {
      assetType: props.assetType,
      date: requestDate.value,
      cursor: append ? nextCursor.value : '',
      limit: 100,
    })
    if (version !== requestVersion) return
    if (!hasUsableEnvelopeData(result)) {
      if (tradesSucceeded) tradesEnvelope.value = markEnvelopeStale(tradesEnvelope.value, result.errors?.[0] || `数据状态：${result.status}`)
      else tradesEnvelope.value = result
      return
    }
    const incoming = rowsFrom(result.data)
    tradeRows.value = append ? [...tradeRows.value, ...incoming] : incoming
    nextCursor.value = String(result.data?.nextCursor || result.meta?.nextCursor || '')
    const resultData = result.data && !Array.isArray(result.data) ? result.data : {}
    tradesEnvelope.value = {...result, data: {...resultData, rows: tradeRows.value}}
    tradesError.value = ''
    tradesSucceeded = true
  } catch (reason) {
    if (version !== requestVersion) return
    tradesError.value = reason?.message || String(reason)
    tradesEnvelope.value = tradesSucceeded
      ? markEnvelopeStale(tradesEnvelope.value, reason)
      : {...markEnvelopeStale({data: {rows: []}}, reason), status: 'unavailable', stale: false}
  } finally {
    if (version === requestVersion) tradesLoading.value = false
  }
}

function loadActiveTab() {
  if (!visible.value) return
  if (activeTab.value === 'auction') void loadAuction()
  else void loadTrades()
}

watch(() => props.defaultTab, value => { if (value) activeTab.value = value })
watch(instrumentIdentity, () => {
  resetInstrumentState()
  loadActiveTab()
})
watch([visible, activeTab], () => {
  requestVersion += 1
  if (!visible.value) return
  loadActiveTab()
}, {immediate: true})
</script>

<template>
  <n-drawer v-model:show="visible" :width="900" placement="right">
    <n-drawer-content :title="`${name || code} · 集合竞价与逐笔成交`" closable>
      <n-tabs v-model:value="activeTab" type="line">
        <n-tab-pane name="auction" tab="集合竞价">
          <EvidenceStatusBar :envelope="auctionEnvelope" :error="auctionError" :loading="auctionLoading" @refresh="loadAuction"/>
          <n-grid :cols="6" :x-gap="10" :y-gap="10" style="margin-bottom: 12px">
            <n-gi><n-statistic label="指示价格" :value="firstValue(auctionSummary, ['price', 'matchedPrice', 'matchPrice', 'indicativePrice'], '--')"/></n-gi>
            <n-gi><n-statistic label="匹配量" :value="firstValue(auctionSummary, ['matchedVolume', 'matchVolume'], '--')"/></n-gi>
            <n-gi><n-statistic label="未匹配量" :value="formatOptionalNumber(auctionSummary, ['unmatchedVolume', 'unmatchVolume'])"/></n-gi>
            <n-gi><n-statistic label="匹配金额" :value="firstValue(auctionSummary, ['matchedAmount', 'matchAmount'], '--')"/></n-gi>
            <n-gi><n-statistic label="竞价强度" :value="formatOptionalMetric(auctionData, ['auctionStrength'], {digits: 2, signed: true, suffix: '%'})"/></n-gi>
            <n-gi><n-statistic label="竞价缺口" :value="formatOptionalMetric(auctionData, ['gapPct'], {digits: 2, signed: true, suffix: '%'})"/></n-gi>
          </n-grid>
          <n-data-table :columns="auctionColumns" :data="auctionRows" :loading="auctionLoading && !auctionRows.length" :max-height="600"/>
          <n-empty v-if="!auctionLoading && !auctionRows.length" description="暂无集合竞价明细"/>
        </n-tab-pane>
        <n-tab-pane name="trades" tab="逐笔成交">
          <EvidenceStatusBar :envelope="tradesEnvelope" :error="tradesError" :loading="tradesLoading" @refresh="() => loadTrades()"/>
          <n-data-table :columns="tradeColumns" :data="tradeRows" :loading="tradesLoading && !tradeRows.length" :max-height="650"/>
          <n-empty v-if="!tradesLoading && !tradeRows.length" description="暂无逐笔成交数据"/>
          <n-flex justify="center" style="margin-top: 12px">
            <n-button v-if="nextCursor" :loading="tradesLoading" @click="loadTrades({append:true})">加载更多</n-button>
          </n-flex>
        </n-tab-pane>
      </n-tabs>
    </n-drawer-content>
  </n-drawer>
</template>
