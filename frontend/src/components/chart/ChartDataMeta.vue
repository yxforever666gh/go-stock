<script setup>
import {computed} from 'vue'

const props = defineProps({
  model: {type: Object, default: () => ({})},
  loading: {type: Boolean, default: false},
  error: {type: String, default: ''},
})
defineEmits(['refresh'])

const status = computed(() => {
  if (props.loading && !props.model?.fetchedAt) return {label: '加载中', type: 'info'}
  if (props.model?.status === 'unavailable') return {label: '不可用', type: 'error'}
  if (props.model?.status === 'after_cutoff') return {label: '截止后', type: 'warning'}
  if (props.model?.status === 'partial' || props.model?.status === 'stale') return {label: props.model.status === 'stale' ? '已过期' : '部分数据', type: 'warning'}
  return {label: '数据完整', type: 'success'}
})
const sources = computed(() => {
  const values = props.model?.sources?.length ? props.model.sources : [props.model?.source]
  return [...new Set(values.flatMap(value => typeof value === 'string' ? [value] : [value?.provider || value?.name || value?.source]).filter(Boolean))]
})
const errors = computed(() => [props.error, ...(props.model?.errors || []).map(value => {
  if (value?.provider && value?.message) return `${value.provider}：${value.message}`
  return value?.message || String(value)
})].filter(Boolean))

function dateTime(value) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return String(value).replace('T', ' ').slice(0, 19)
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date).replaceAll('/', '-')
}
</script>

<template>
  <div class="chart-data-meta">
    <n-flex align="center" :wrap="true" :size="8">
      <n-tag size="small" :bordered="false" :type="status.type">{{ status.label }}</n-tag>
      <n-text depth="3">来源：{{ sources.join('、') || '未标注' }}</n-text>
      <n-text depth="3">数据截至：{{ dateTime(model?.asOf) }}</n-text>
      <n-text depth="3">采集于：{{ dateTime(model?.fetchedAt) }}</n-text>
      <n-tag v-if="model?.missingIntervals?.length" size="small" type="warning" :bordered="false">
        缺失区间 {{ model.missingIntervals.length }}
      </n-tag>
      <n-popover v-if="errors.length" trigger="hover">
        <template #trigger><n-tag size="small" type="error" :bordered="false">异常 {{ errors.length }}</n-tag></template>
        <n-list size="small" style="max-width: 520px">
          <n-list-item v-for="item in errors" :key="item">{{ item }}</n-list-item>
        </n-list>
      </n-popover>
      <n-button size="tiny" secondary :loading="loading" @click="$emit('refresh')">刷新</n-button>
    </n-flex>
    <n-collapse v-if="model?.missingIntervals?.length" arrow-placement="right" class="gap-list">
      <n-collapse-item title="查看缺失区间" name="gaps">
        <n-text v-for="item in model.missingIntervals" :key="`${item.from}-${item.to}-${item.reason}`" tag="div" depth="3">
          {{ dateTime(item.from) }} — {{ dateTime(item.to) }}：{{ item.reason || '数据缺失' }}
        </n-text>
      </n-collapse-item>
    </n-collapse>
  </div>
</template>

<style scoped>
.chart-data-meta {
  padding: 7px 9px;
  border: 1px solid rgba(128, 128, 128, .2);
  border-radius: 6px;
}

.gap-list {
  margin-top: 4px;
}
</style>
