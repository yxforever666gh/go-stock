<script setup>
import {computed, h, onBeforeUnmount, onMounted, ref} from 'vue'
import {NButton, NTag, useMessage} from 'naive-ui'
import {MdPreview} from 'md-editor-v3'
import {GetAIAnalysisReport, ListAIAnalysisReports, StartAIAnalysis} from '../services/app-api'

const message = useMessage()
const loading = ref(false)
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)
const detailRunID = ref('')
const starting = ref(false)
let waitingForManualRun = false
let manualBaselineRunID = ''
let pollTimer = null

const hasRunningReport = computed(() => rows.value.some(row => row.status === 'running'))
const analysisBusy = computed(() => starting.value || hasRunningReport.value)

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

function summarySourceStatus(row) {
  const total = Number(row?.sourceCount || 0)
  const failed = Number(row?.failedSourceCount || 0)
  return `${total} 个来源，${failed} 个失败`
}

function sourceRows(value) {
  try {
    const rows = JSON.parse(value || '[]')
    return Array.isArray(rows) ? rows.map(item => ({
      sourceId: item.sourceId,
      sourceName: item.sourceName,
      category: item.category,
      collectedAt: item.collectedAt,
      status: item.error ? '失败' : '成功',
      error: item.error || '',
    })) : []
  } catch (_) {
    return []
  }
}

function attemptRows(value) {
  try {
    const records = JSON.parse(value || '[]')
    return Array.isArray(records) ? records : []
  } catch (_) {
    return []
  }
}

const attemptStatusLabels = {
  waiting_response: '等待响应', waiting: '等待响应', reasoning: '推理中', streaming: '生成中',
  success: '成功', failed: '失败', cancelled: '已取消',
}

const attemptActionLabels = {
  retry_same_model: '重试当前模型', fallback_next_model: '顺延下一模型', stop: '终止', complete: '完成',
}

function attemptStatusType(status) {
  if (status === 'success') return 'success'
  if (status === 'failed' || status === 'cancelled') return 'error'
  if (status === 'reasoning' || status === 'streaming' || status === 'waiting' || status === 'waiting_response') return 'warning'
  return 'default'
}

const attemptColumns = [
  {title: '阶段', key: 'phase', width: 150},
  {title: '模型', key: 'modelName', minWidth: 190, render: row => `${row.providerName || '--'} / ${row.modelName || '--'}`},
  {title: 'API 格式', key: 'apiProtocol', width: 145},
  {title: '次数', key: 'attempt', width: 80, render: row => `${row.attempt}/${row.maxAttempts}`},
  {title: '状态', key: 'status', width: 110, render: row => h(NTag, {type: attemptStatusType(row.status), bordered: false}, {default: () => attemptStatusLabels[row.status] || row.status})},
  {title: '最后事件', key: 'lastEventType', minWidth: 180, ellipsis: {tooltip: true}, render: row => row.lastEventType || '--'},
  {title: '最后活动', key: 'lastActivityAt', width: 170, render: row => dateTime(row.lastActivityAt)},
  {title: '耗时', key: 'durationMs', width: 90, render: row => `${(Number(row.durationMs || 0) / 1000).toFixed(1)}s`},
  {title: '错误', key: 'errorMessage', minWidth: 260, ellipsis: {tooltip: true}, render: row => row.errorMessage ? `[${row.errorCategory || 'error'}] ${row.errorMessage}` : '--'},
  {title: '后续动作', key: 'nextAction', width: 130, render: row => attemptActionLabels[row.nextAction] || row.nextAction || '--'},
]

const sourceColumns = [
  {title: '编号', key: 'sourceId', width: 90},
  {title: '来源', key: 'sourceName', minWidth: 150},
  {title: '分类', key: 'category', width: 110},
  {title: '采集时间', key: 'collectedAt', width: 170, render: row => dateTime(row.collectedAt)},
  {title: '状态', key: 'status', width: 80, render: row => h(NTag, {type: row.error ? 'error' : 'success', bordered: false}, {default: () => row.status})},
  {title: '失败原因', key: 'error', minWidth: 220, ellipsis: {tooltip: true}, render: row => row.error || '--'},
]

const columns = [
  {title: '计划时间', key: 'scheduledFor', width: 170, render: row => dateTime(row.scheduledFor)},
  {title: '完成时间', key: 'completedAt', width: 170, render: row => dateTime(row.completedAt)},
  {title: 'Provider / 模型', key: 'modelName', minWidth: 190, render: row => `${row.providerName || '--'} / ${row.modelName || '--'}`},
  {title: '状态', key: 'status', width: 130, render: row => h(NTag, {type: statusType(row.status), bordered: false}, {default: () => statusLabels[row.status] || row.status})},
  {title: '推荐数', key: 'recommendationCount', width: 90},
  {title: '来源状态', key: 'sourceCount', minWidth: 150, render: row => summarySourceStatus(row)},
  {title: '空仓/失败原因', key: 'failureReason', minWidth: 210, ellipsis: {tooltip: true}, render: row => row.failureReason || '--'},
  {title: '操作', key: 'actions', width: 110, render: row => h(NButton, {size: 'small', tertiary: true, type: 'primary', onClick: () => showDetail(row)}, {default: () => '查看报告'})},
]

function stopPolling() {
  if (pollTimer !== null) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
}

function schedulePolling() {
  stopPolling()
  if (!hasRunningReport.value && !waitingForManualRun) return
  pollTimer = setTimeout(() => refresh(true), 2000)
}

async function refresh(silent = false) {
  if (loading.value) return
  loading.value = true
  try {
    rows.value = await ListAIAnalysisReports(100, 0) || []
		const newest = rows.value[0]
		const manualRunObserved = waitingForManualRun && newest && newest.runId !== manualBaselineRunID
		if (manualRunObserved && newest.status !== 'running') {
			waitingForManualRun = false
			starting.value = false
			message.success('AI 分析报告已生成')
		}
		if (detailVisible.value && detailRunID.value && (!detail.value || detail.value.status === 'running')) {
			await refreshDetail(true)
		}
  } catch (error) {
		if (!silent) message.error(error?.message || String(error))
  } finally {
    loading.value = false
		schedulePolling()
  }
}

async function startAnalysis() {
	if (analysisBusy.value) return
	manualBaselineRunID = rows.value[0]?.runId || ''
	starting.value = true
	waitingForManualRun = true
	try {
		await StartAIAnalysis()
		message.success('AI 分析已开始')
		await refresh(true)
	} catch (error) {
		waitingForManualRun = false
		starting.value = false
		message.error(error?.message || String(error))
	}
}

async function showDetail(row) {
  detailVisible.value = true
  detail.value = null
	detailRunID.value = row.runId
	await refreshDetail(false)
}

async function refreshDetail(silent = false) {
  if (!detailRunID.value) return
  try {
    detail.value = await GetAIAnalysisReport(detailRunID.value)
  } catch (error) {
    if (!silent) message.error(error?.message || String(error))
  }
}

onMounted(refresh)
onBeforeUnmount(stopPolling)
</script>

<template>
  <n-space vertical>
    <n-flex justify="space-between" align="center">
			<n-button type="primary" :disabled="analysisBusy" :loading="analysisBusy" @click="startAnalysis">
				{{ analysisBusy ? 'AI 分析中…' : '开始 AI 分析' }}
			</n-button>
			<n-button :loading="loading" @click="refresh(false)">刷新</n-button>
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
              <n-collapse-item title="模型调用记录" name="attempts">
                <n-data-table :columns="attemptColumns" :data="attemptRows(detail.modelAttemptLogJson)" :scroll-x="1650" size="small"/>
              </n-collapse-item>
              <n-collapse-item title="来源状态" name="sources">
                <n-data-table :columns="sourceColumns" :data="sourceRows(detail.sourceStatusJson)" :scroll-x="900" size="small"/>
              </n-collapse-item>
            </n-collapse>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>
