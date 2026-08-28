<script setup>
import {computed, toRef} from 'vue'
import {GetMarketBreadth} from '../services/market-api.js'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import {formatOptionalMetric, numberValue} from '../market-tabs/market-data.js'
import EvidenceStatusBar from './EvidenceStatusBar.vue'

const props = defineProps({active: {type: Boolean, default: false}})
const active = toRef(props, 'active')
const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {},
  intervalMs: 30000,
  loader: GetMarketBreadth,
})

const breadth = computed(() => data.value?.summary || data.value || {})
const usable = computed(() => ['ok', 'partial', 'stale'].includes(String(envelope.value?.status || ''))
  && numberValue(breadth.value, ['total', 'totalCount', 'sampleCount']) > 0)

function breadthValue(keys) {
  return usable.value ? numberValue(breadth.value, keys) : '—'
}

function optionalBreadthValue(keys, options = {}) {
  return usable.value ? formatOptionalMetric(breadth.value, keys, options) : '—'
}

const metrics = computed(() => [
  {label: '全市场', value: breadthValue(['total', 'totalCount', 'sampleCount']), type: 'info'},
  {label: '上涨', value: breadthValue(['advances', 'advanceCount', 'upCount', 'advancers']), type: 'error'},
  {label: '下跌', value: breadthValue(['declines', 'declineCount', 'downCount', 'decliners']), type: 'success'},
  {label: '平盘', value: breadthValue(['flat', 'flatCount', 'unchangedCount']), type: 'default'},
  {label: '涨停', value: breadthValue(['limitUps', 'limitUpCount', 'upLimitCount']), type: 'error'},
  {label: '跌停', value: breadthValue(['limitDowns', 'limitDownCount', 'downLimitCount']), type: 'success'},
  {label: '日内新高', value: optionalBreadthValue(['newHighs', 'newHighCount']), type: 'error', unit: '只'},
  {label: '日内新低', value: optionalBreadthValue(['newLows', 'newLowCount']), type: 'success', unit: '只'},
  {label: '涨跌中位数', value: optionalBreadthValue(['medianChangePct', 'medianChangePercent'], {digits: 2, signed: true}), type: 'info', unit: '%'},
])

const riseRatio = computed(() => {
  if (!usable.value) return null
  const total = metrics.value[0].value
  const up = metrics.value[1].value
  return total > 0 ? Math.min(100, Math.max(0, up / total * 100)) : null
})
</script>

<template>
  <n-card size="small" title="A股市场宽度" class="market-breadth">
    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
    <n-grid :cols="9" :x-gap="10" :y-gap="10" responsive="screen">
      <n-gi v-for="item in metrics" :key="item.label">
        <n-statistic :label="item.label" :value="item.value">
          <template #suffix><n-tag size="tiny" :type="item.type" :bordered="false">{{ item.unit || '只' }}</n-tag></template>
        </n-statistic>
      </n-gi>
    </n-grid>
    <n-flex align="center" :wrap="false" class="breadth-scale">
      <n-text depth="3">上涨占比</n-text>
      <n-progress type="line" :percentage="riseRatio ?? 0" :show-indicator="false" :status="riseRatio === null ? 'default' : 'success'"/>
      <n-text>{{ riseRatio === null ? '—' : `${riseRatio.toFixed(1)}%` }}</n-text>
    </n-flex>
  </n-card>
</template>

<style scoped>
.market-breadth {
  margin-bottom: 10px;
  text-align: left;
}

.breadth-scale {
  gap: 12px;
  margin-top: 12px;
}

.breadth-scale :deep(.n-progress) {
  flex: 1;
}
</style>
