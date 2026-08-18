<script setup>
import {computed, h} from 'vue'

const props = defineProps({detail: {type: Object, required: true}})

const statusLabel = {ready: '数据完整', partial: '部分来源失败', critical_failed: '关键数据失败'}
const statusType = status => status === 'ready' ? 'success' : status === 'partial' ? 'warning' : 'error'
const dateTime = value => value ? String(value).slice(0, 19).replace('T', ' ') : '--'

function parseJSON(value, fallback) {
  try { return JSON.parse(value || '') }
  catch { return fallback }
}

function quoteOf(item) { return parseJSON(item.quoteJson, {}) }
function minuteOf(item) { return parseJSON(item.minuteSummaryJson, {}) }
function sourcesOf(item) { return parseJSON(item.evidenceJson, []) }
function sourceRefsOf(item) { return parseJSON(item.sourceRefs, []) }
function percent(value) { return Number.isFinite(Number(value)) ? `${(Number(value) * 100).toFixed(2)}%` : '--' }
function number(value, digits = 2) { return Number.isFinite(Number(value)) ? Number(value).toFixed(digits) : '--' }
function duration(seconds) {
  const total = Math.max(0, Number(seconds) || 0)
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  return `${hours}小时${minutes}分钟`
}
function readableContent(value) {
  if (!value) return '无新增内容'
  const parsed = parseJSON(value, null)
  return parsed === null ? value : JSON.stringify(parsed, null, 2)
}
function sourceContent(row) {
  return h('pre', {style: {margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxHeight: '180px', overflow: 'auto'}}, row.error || readableContent(row.content))
}

const pendingSummary = computed(() => {
  if (props.detail?.recommendation?.status !== 'pending') return null
  return {
    remaining: duration(props.detail.activationRemainingSeconds),
    elapsed: duration(props.detail.activationTradingElapsedSeconds),
    pause: duration(props.detail.recommendation.dataPauseSeconds),
    next: dateTime(props.detail.recommendation.nextCheckAt),
  }
})
</script>

<template>
  <n-alert v-if="pendingSummary" type="info" style="margin-bottom: 12px">
    已累计 {{ pendingSummary.elapsed }}，剩余激活交易时长 {{ pendingSummary.remaining }}；关键数据故障已抵扣
    {{ pendingSummary.pause }}，下次检查 {{ pendingSummary.next }}。
  </n-alert>

  <n-empty v-if="!(detail.observations || []).length" description="尚无生命周期数据快照" size="small"/>
  <n-collapse v-else accordion>
    <n-collapse-item v-for="item in [...detail.observations].reverse()" :key="item.observationId" :name="item.observationId">
      <template #header>
        <n-flex align="center" :wrap="false">
          <n-text>{{ dateTime(item.observedAt) }} · {{ item.phase === 'holding' ? '持仓判断' : '激活判断' }}</n-text>
          <n-tag size="small" :type="statusType(item.status)" :bordered="false">{{ statusLabel[item.status] || item.status }}</n-tag>
          <n-tag size="small" :type="item.modelInvoked ? 'info' : 'default'" :bordered="false">{{ item.modelInvoked ? '已调用 AI' : '未调用 AI' }}</n-tag>
        </n-flex>
      </template>

      <n-alert v-if="item.criticalFailure" type="error" style="margin-bottom: 10px">{{ item.criticalFailure }}</n-alert>
      <n-descriptions bordered :column="3" size="small">
        <n-descriptions-item label="观察窗口" :span="3">{{ dateTime(item.windowFrom) }} — {{ dateTime(item.observedAt) }}</n-descriptions-item>
        <n-descriptions-item label="最新价格">{{ number(quoteOf(item).price, 3) }}</n-descriptions-item>
        <n-descriptions-item label="涨跌幅">{{ percent((quoteOf(item).price - quoteOf(item).previousClose) / quoteOf(item).previousClose) }}</n-descriptions-item>
        <n-descriptions-item label="行情时间">{{ dateTime(quoteOf(item).at) }}</n-descriptions-item>
        <n-descriptions-item label="分钟最新价">{{ number(minuteOf(item).latestPrice, 3) }}</n-descriptions-item>
        <n-descriptions-item label="分钟最新时间">{{ dateTime(minuteOf(item).latestAt) }}</n-descriptions-item>
        <n-descriptions-item label="有效分钟记录">{{ minuteOf(item).totalBars || 0 }}</n-descriptions-item>
      </n-descriptions>

      <n-data-table v-if="(minuteOf(item).windows || []).length" style="margin-top: 10px" size="small" :columns="[
        {title:'窗口', key:'minutes', render:row => `${row.minutes} 分钟`},
        {title:'记录数', key:'bars'},
        {title:'区间涨跌', key:'returnRate', render:row => percent(row.returnRate)},
        {title:'最高', key:'high', render:row => number(row.high, 3)},
        {title:'最低', key:'low', render:row => number(row.low, 3)},
        {title:'成交量', key:'volume', render:row => number(row.volume, 0)},
        {title:'均价', key:'averagePrice', render:row => number(row.averagePrice, 3)}
      ]" :data="minuteOf(item).windows" :row-key="row => row.minutes"/>

      <n-data-table style="margin-top: 10px" size="small" :single-line="false" :columns="[
        {title:'来源编号', key:'id', width:165},
        {title:'来源', key:'name', width:170},
        {title:'状态', key:'status', width:100},
        {title:'内容/错误', key:'content', render:sourceContent}
      ]" :data="sourcesOf(item)" :row-key="row => row.id" :max-height="280"/>
    </n-collapse-item>
  </n-collapse>

  <n-divider title-placement="left">AI 决策记录</n-divider>
  <n-timeline>
    <n-timeline-item v-for="item in detail.decisions" :key="item.eventId" :type="item.decisionType === '错误重试' || item.dataStatus === 'critical_failed' ? 'error' : 'info'" :title="item.decisionType" :time="dateTime(item.decidedAt)">
      <n-space vertical size="small">
        <n-text>{{ item.reason }}</n-text>
        <n-flex v-if="sourceRefsOf(item).length" align="center">
          <n-text depth="3">引用：</n-text>
          <n-tag v-for="source in sourceRefsOf(item)" :key="source" size="small" type="info" :bordered="false">{{ source }}</n-tag>
        </n-flex>
      </n-space>
    </n-timeline-item>
  </n-timeline>
</template>
