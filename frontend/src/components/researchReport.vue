<script setup>
import {computed, h, onBeforeUnmount, onMounted, ref} from 'vue'
import {NButton, NTag, useMessage} from 'naive-ui'
import {formatInteger, formatMoney, formatNumber, formatPercent, formatPrice} from '../utils/number-format'
import AppMarkdownPreview from './AppMarkdownPreview.vue'
import ResearchAuditPanel from './research-audit/ResearchAuditPanel.vue'
import {
  GetAIAnalysisReport,
  GetAICapitalDeploymentStatus,
  ListAIAnalysisReports,
  ListAIBuyOpportunities,
} from '../services/research-api'
import {CreateKnowledgeFromResearch, CreateKnowledgeMemoryCandidate} from '../services/knowledge-api'

const message = useMessage()
const loading = ref(false)
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)
const detailRunID = ref('')
const deploymentStatus = ref(null)
const opportunities = ref([])
const savingKnowledgeDraft = ref(false)
const creatingMemoryCandidate = ref(false)
let pollTimer = null

const hasRunningReport = computed(() => rows.value.some(row => row.status === 'running'))

const statusLabels = {
  running: '分析中', success: '已推荐', no_recommendation: '空仓', failed: '失败',
  skipped_non_trading_day: '非交易日跳过', skipped_open_position: '持仓中跳过', skipped_capacity: '容量不足跳过', skipped_cash: '现金不足跳过',
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
    return `${formatInteger(sources.length)} 个来源，${formatInteger(failed.length)} 个失败`
  } catch (_) {
    return '--'
  }
}

function summarySourceStatus(row) {
  const total = Number(row?.sourceCount || 0)
  const failed = Number(row?.failedSourceCount || 0)
  return `${formatInteger(total)} 个来源，${formatInteger(failed)} 个失败`
}

function runOrigin(row) {
	const source = String(row?.triggerSource || '').trim()
	if (!source) return '历史定时运行'
	const labels = {sell: '卖出触发', startup_recovery: '启动恢复', capital_gap: '资金缺口'}
	return source.split(',').map(item => labels[item.trim()] || item.trim()).filter(Boolean).join(' / ')
}

function decisionSummary(row) {
  if (row?.buyNowCount == null && row?.waitCount == null && row?.rejectCount == null) return '历史无结构化候选'
  return `立即 ${formatInteger(row?.buyNowCount)} / 等待 ${formatInteger(row?.waitCount)} / 放弃 ${formatInteger(row?.rejectCount)}`
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
  {title: '次数', key: 'attempt', width: 90, render: row => `${formatInteger(row.attempt)}/${formatInteger(row.maxAttempts)}`},
  {title: '状态', key: 'status', width: 110, render: row => h(NTag, {type: attemptStatusType(row.status), bordered: false}, {default: () => attemptStatusLabels[row.status] || row.status})},
  {title: '最后事件', key: 'lastEventType', minWidth: 180, ellipsis: {tooltip: true}, render: row => row.lastEventType || '--'},
  {title: '最后活动', key: 'lastActivityAt', width: 170, render: row => dateTime(row.lastActivityAt)},
  {title: '耗时', key: 'durationMs', width: 100, render: row => `${formatNumber(Number(row.durationMs || 0) / 1000, 1)}s`},
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

const decisionLabels = {buy_now: '立即买入', wait: '等待', reject: '放弃'}
const decisionTypes = {buy_now: 'success', wait: 'warning', reject: 'default'}
const opportunityColumns = [
  {title: '决策', key: 'action', width: 105, render: row => h(NTag, {type: decisionTypes[row.action] || 'default', bordered: false}, {default: () => decisionLabels[row.action] || row.action})},
  {title: '股票', key: 'stockCode', width: 160, render: row => `${row.stockName || '--'} (${row.stockCode || '--'})`},
  {title: '价格区间', key: 'priceLow', width: 155, render: row => row.priceLow || row.priceHigh ? `${formatPrice(row.priceLow)} - ${formatPrice(row.priceHigh)}` : '--'},
  {title: '理由', key: 'aiSummary', minWidth: 260, ellipsis: {tooltip: true}, render: row => row.timingReason || row.aiSummary || '--'},
  {title: '风险', key: 'mainRisk', minWidth: 220, ellipsis: {tooltip: true}, render: row => row.mainRisk || '--'},
]

const columns = [
  {title: '触发来源', key: 'triggerSource', width: 150, render: row => runOrigin(row)},
  {title: '触发原因', key: 'triggerReason', minWidth: 180, ellipsis: {tooltip: true}, render: row => row.triggerReason || '--'},
  {title: '完成时间', key: 'completedAt', width: 170, render: row => dateTime(row.completedAt)},
  {title: 'Provider / 模型', key: 'modelName', minWidth: 190, render: row => `${row.providerName || '--'} / ${row.modelName || '--'}`},
  {title: '状态', key: 'status', width: 130, render: row => h(NTag, {type: statusType(row.status), bordered: false}, {default: () => statusLabels[row.status] || row.status})},
  {title: '候选决策', key: 'buyNowCount', width: 215, render: row => decisionSummary(row)},
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
  if (!hasRunningReport.value) return
  pollTimer = setTimeout(() => refresh(true), 2000)
}

async function refresh(silent = false) {
  if (loading.value) return
  loading.value = true
  try {
		const [reportsResult, statusResult] = await Promise.allSettled([
			ListAIAnalysisReports(100, 0),
			GetAICapitalDeploymentStatus(),
		])
		if (reportsResult.status === 'rejected') throw reportsResult.reason
		rows.value = reportsResult.value || []
		if (statusResult.status === 'fulfilled') deploymentStatus.value = statusResult.value || null
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

async function showDetail(row) {
  detailVisible.value = true
  detail.value = null
	opportunities.value = []
	detailRunID.value = row.runId
	await refreshDetail(false)
}

async function refreshDetail(silent = false) {
  if (!detailRunID.value) return
  try {
		const [reportResult, opportunitiesResult] = await Promise.allSettled([
			GetAIAnalysisReport(detailRunID.value),
			ListAIBuyOpportunities(200, 0),
		])
		if (reportResult.status === 'rejected') throw reportResult.reason
		detail.value = reportResult.value
		const allOpportunities = opportunitiesResult.status === 'fulfilled' ? opportunitiesResult.value : []
		opportunities.value = Array.isArray(reportResult.value?.opportunities)
			? reportResult.value.opportunities
			: (allOpportunities || []).filter(item => item.analysisRunId === detailRunID.value)
  } catch (error) {
    if (!silent) message.error(error?.message || String(error))
  }
}

async function saveKnowledgeDraft() {
  const runId = String(detail.value?.runId || detailRunID.value || '')
  if (!runId || savingKnowledgeDraft.value) return
  savingKnowledgeDraft.value = true
  try {
    await CreateKnowledgeFromResearch({
      sourceOwnerType: 'research1',
      sourceOwnerId: runId,
      title: `AI 分析报告 ${dateTime(detail.value?.completedAt || detail.value?.startedAt)}`,
    })
    message.success('报告已保存为知识草稿，批准前不会进入研究检索')
  } catch (error) {
    message.error(error?.message || String(error))
  } finally {
    savingKnowledgeDraft.value = false
  }
}

async function createMemoryCandidate() {
  const runId = String(detail.value?.runId || detailRunID.value || '')
  if (!runId || creatingMemoryCandidate.value) return
  creatingMemoryCandidate.value = true
  try {
    await CreateKnowledgeMemoryCandidate({
      sourceOwnerType: 'research1',
      sourceOwnerId: runId,
      title: `AI 分析记忆候选 ${dateTime(detail.value?.completedAt || detail.value?.startedAt)}`,
    })
    message.success('已从报告生成记忆候选；必须由用户批准后才能进入研究检索')
  } catch (error) {
    message.error(error?.message || String(error))
  } finally {
    creatingMemoryCandidate.value = false
  }
}

onMounted(refresh)
onBeforeUnmount(stopPolling)
</script>

<template>
  <n-space vertical>
    <n-flex justify="space-between" align="center">
			<n-alert v-if="deploymentStatus" :type="deploymentStatus.enabled ? 'info' : 'warning'" :show-icon="false" style="flex: 1">
				资金补位{{ deploymentStatus.enabled ? '已启用' : '已停用' }} · 现金 {{ formatMoney(deploymentStatus.cash) }} · 保留 {{ formatMoney(deploymentStatus.reserveTarget) }} · 可部署 {{ formatMoney(deploymentStatus.deployableCash) }} · 资金利用率 {{ formatPercent(deploymentStatus.capitalUtilization) }} · 待处理事件 {{ formatInteger(deploymentStatus.pendingEventCount) }} · 观察候选 {{ formatInteger(deploymentStatus.watchingCandidateCount) }} · 下次分析 {{ dateTime(deploymentStatus.nextEligibleAt) }} · {{ deploymentStatus.reason || '等待事件' }}
			</n-alert>
			<n-button :loading="loading" @click="refresh(false)">刷新</n-button>
    </n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="1600" :row-key="row => row.runId"/>
  </n-space>

  <n-modal v-model:show="detailVisible">
    <n-card style="width:min(1380px, 96vw); max-height:92vh" title="AI 分析报告" closable @close="detailVisible = false">
      <n-scrollbar style="max-height:78vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <ResearchAuditPanel owner-type="research1" :owner-id="String(detail.runId || detailRunID)" :active="detailVisible">
              <template #final-result>
                <n-descriptions bordered :column="3" size="small">
                  <n-descriptions-item label="运行状态">{{ statusLabels[detail.status] || detail.status }}</n-descriptions-item>
                  <n-descriptions-item label="模型">{{ detail.providerName }} / {{ detail.modelName }}</n-descriptions-item>
                  <n-descriptions-item label="来源">{{ sourceSummary(detail.sourceStatusJson) }}</n-descriptions-item>
							<n-descriptions-item label="触发来源">{{ runOrigin(detail) }}</n-descriptions-item>
							<n-descriptions-item label="触发原因">{{ detail.triggerReason || '--' }}</n-descriptions-item>
							<n-descriptions-item label="候选决策">{{ decisionSummary(detail) }}</n-descriptions-item>
                </n-descriptions>
                <n-flex justify="end" style="margin-top: 10px">
                  <n-button secondary type="warning" :loading="creatingMemoryCandidate" @click="createMemoryCandidate">生成记忆候选</n-button>
                  <n-button secondary type="primary" :loading="savingKnowledgeDraft" @click="saveKnowledgeDraft">保存为知识草稿</n-button>
                </n-flex>
                <n-divider title-placement="left">完整决策报告</n-divider>
                <AppMarkdownPreview :model-value="detail.finalReport || detail.failureReason || '暂无报告'"/>
				<n-divider title-placement="left">立即买入 / 等待 / 放弃候选</n-divider>
				<n-empty v-if="!opportunities.length" description="本轮没有结构化候选；历史分析可能不包含此数据"/>
				<n-data-table v-else :columns="opportunityColumns" :data="opportunities" :scroll-x="1000" size="small" :row-key="row => row.opportunityId"/>
                <n-collapse>
                  <n-collapse-item title="大盘层" name="market"><AppMarkdownPreview :model-value="detail.marketReport || '无'"/></n-collapse-item>
                  <n-collapse-item title="板块层" name="sector"><AppMarkdownPreview :model-value="detail.sectorReport || '无'"/></n-collapse-item>
                  <n-collapse-item title="个股层" name="stock"><AppMarkdownPreview :model-value="detail.stockReport || '无'"/></n-collapse-item>
                  <n-collapse-item title="模型调用记录" name="attempts">
                    <n-data-table :columns="attemptColumns" :data="attemptRows(detail.modelAttemptLogJson)" :scroll-x="1650" size="small"/>
                  </n-collapse-item>
                  <n-collapse-item title="来源状态" name="sources">
                    <n-data-table :columns="sourceColumns" :data="sourceRows(detail.sourceStatusJson)" :scroll-x="900" size="small"/>
                  </n-collapse-item>
                </n-collapse>
              </template>
            </ResearchAuditPanel>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>
