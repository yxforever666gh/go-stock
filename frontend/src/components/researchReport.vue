<script setup>
import {h, onMounted, ref} from 'vue'
import {NButton, NTag, NText, useMessage} from 'naive-ui'
import {MdPreview} from 'md-editor-v3'
import {GetAIAnalysisReport, ListAIAnalysisReports} from '../services/app-api'

const message = useMessage()
const loading = ref(false)
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)

const statusLabels = {
  running: '分析中', success: '已推荐', no_recommendation: '空仓', failed: '失败',
  skipped_non_trading_day: '非交易日跳过', skipped_open_position: '持仓中跳过',
}

function dateTime(value) {
  return value ? String(value).slice(0, 19).replace('T', ' ') : '--'
}

function statusType(status) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'error'
  if (status === 'running') return 'warning'
  return 'info'
}

function sourceSummary(value) {
  try {
    const sources = JSON.parse(value || '[]')
    const failed = sources.filter(item => item.error)
    return `${sources.length} 个来源，${failed.length} 个失败`
  } catch (_) {
    return '--'
  }
}

const columns = [
  {title: '计划时间', key: 'scheduledFor', width: 170, render: row => dateTime(row.scheduledFor)},
  {title: '完成时间', key: 'completedAt', width: 170, render: row => dateTime(row.completedAt)},
  {title: 'Provider / 模型', key: 'modelName', minWidth: 190, render: row => `${row.providerName || '--'} / ${row.modelName || '--'}`},
  {title: '状态', key: 'status', width: 130, render: row => h(NTag, {type: statusType(row.status), bordered: false}, {default: () => statusLabels[row.status] || row.status})},
  {title: '推荐数', key: 'recommendationCount', width: 90},
  {title: '来源状态', key: 'sourceStatusJson', minWidth: 150, render: row => sourceSummary(row.sourceStatusJson)},
  {title: '空仓/失败原因', key: 'failureReason', minWidth: 210, ellipsis: {tooltip: true}, render: row => row.failureReason || '--'},
  {title: '操作', key: 'actions', width: 110, render: row => h(NButton, {size: 'small', tertiary: true, type: 'primary', onClick: () => showDetail(row)}, {default: () => '查看报告'})},
]

async function refresh() {
  loading.value = true
  try {
    rows.value = await ListAIAnalysisReports(100, 0) || []
  } catch (error) {
    message.error(error?.message || String(error))
  } finally {
    loading.value = false
  }
}

async function showDetail(row) {
  detailVisible.value = true
  detail.value = null
  try {
    detail.value = await GetAIAnalysisReport(row.runId)
  } catch (error) {
    message.error(error?.message || String(error))
  }
}

onMounted(refresh)
</script>

<template>
  <n-space vertical>
    <n-flex justify="space-between" align="center">
      <n-text depth="3">分级 AI 分析自动运行；页面不提供手动运行入口。</n-text>
      <n-button :loading="loading" @click="refresh">刷新</n-button>
    </n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="1300" :row-key="row => row.runId"/>
  </n-space>

  <n-modal v-model:show="detailVisible">
    <n-card style="width:min(1180px, 94vw); max-height:90vh" title="AI 分析报告" closable @close="detailVisible = false">
      <n-scrollbar style="max-height:78vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <n-descriptions bordered :column="3" size="small">
              <n-descriptions-item label="运行状态">{{ statusLabels[detail.status] || detail.status }}</n-descriptions-item>
              <n-descriptions-item label="模型">{{ detail.providerName }} / {{ detail.modelName }}</n-descriptions-item>
              <n-descriptions-item label="来源">{{ sourceSummary(detail.sourceStatusJson) }}</n-descriptions-item>
            </n-descriptions>
            <n-divider title-placement="left">完整决策报告</n-divider>
            <MdPreview :model-value="detail.finalReport || detail.failureReason || '暂无报告'"/>
            <n-collapse>
              <n-collapse-item title="大盘层" name="market"><MdPreview :model-value="detail.marketReport || '无'"/></n-collapse-item>
              <n-collapse-item title="板块层" name="sector"><MdPreview :model-value="detail.sectorReport || '无'"/></n-collapse-item>
              <n-collapse-item title="个股层" name="stock"><MdPreview :model-value="detail.stockReport || '无'"/></n-collapse-item>
              <n-collapse-item title="来源状态" name="sources"><pre class="source-json">{{ JSON.stringify(JSON.parse(detail.sourceStatusJson || '[]'), null, 2) }}</pre></n-collapse-item>
            </n-collapse>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>

<style scoped>
.source-json { white-space: pre-wrap; word-break: break-word; }
</style>
