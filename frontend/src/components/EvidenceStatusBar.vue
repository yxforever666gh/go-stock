<script setup>
import {computed} from 'vue'

const props = defineProps({
  envelope: {type: Object, default: () => ({})},
  error: {type: String, default: ''},
  loading: {type: Boolean, default: false},
})

defineEmits(['refresh'])

const status = computed(() => {
  if (props.loading && !props.envelope?.fetchedAt) return {label: '加载中', type: 'info'}
  if (props.envelope?.status === 'unavailable') return {label: '不可用', type: 'error'}
  if (props.envelope?.status === 'after_cutoff') return {label: '截止后数据', type: 'warning'}
  if (props.envelope?.stale || props.envelope?.status === 'stale') return {label: '已过期', type: 'warning'}
  if (props.envelope?.partial || props.envelope?.status === 'partial') return {label: '部分数据', type: 'warning'}
  return {label: '数据完整', type: 'success'}
})

const sources = computed(() => {
  const value = props.envelope?.source
  if (Array.isArray(value)) return value.filter(Boolean).join('、')
  return String(value || '未标注')
})

const issues = computed(() => {
  const rows = props.envelope?.errors || []
  const messages = rows.map(item => typeof item === 'string' ? item : (item?.message || JSON.stringify(item))).filter(Boolean)
  if (!messages.length && props.error) messages.push(props.error)
  return [...new Set(messages)]
})

const issueLabel = computed(() => props.envelope?.status === 'unavailable' ? '异常' : '降级')

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
  <n-flex align="center" justify="space-between" :wrap="true" class="evidence-status-bar">
    <n-flex align="center" :wrap="true" :size="8">
      <n-tag size="small" :bordered="false" :type="status.type">{{ status.label }}</n-tag>
      <n-text depth="3">来源：{{ sources }}</n-text>
      <n-text depth="3">数据截至：{{ dateTime(envelope?.asOf) }}</n-text>
      <n-text depth="3">采集于：{{ dateTime(envelope?.fetchedAt) }}</n-text>
      <n-popover v-if="issues.length" trigger="hover" placement="bottom-start">
        <template #trigger>
          <n-tag size="small" :type="envelope?.status === 'unavailable' ? 'error' : 'warning'" :bordered="false">{{ issueLabel }} {{ issues.length }}</n-tag>
        </template>
        <n-list size="small" style="max-width: 520px">
          <n-list-item v-for="item in issues" :key="item">{{ item }}</n-list-item>
        </n-list>
      </n-popover>
    </n-flex>
    <n-button size="small" secondary :loading="loading" @click="$emit('refresh')">刷新</n-button>
  </n-flex>
</template>

<style scoped>
.evidence-status-bar {
  gap: 8px;
  padding: 8px 10px;
  margin-bottom: 10px;
  border: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.22));
  border-radius: 6px;
}
</style>
