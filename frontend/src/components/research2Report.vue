<script setup>
import {h, onMounted, ref} from 'vue'
import {NButton, NTag, useMessage} from 'naive-ui'
import AppMarkdownPreview from './AppMarkdownPreview.vue'
import ResearchAuditPanel from './research-audit/ResearchAuditPanel.vue'
import {GetResearch2Run, ListResearch2Runs} from '../services/research2-api'

const message = useMessage(), loading = ref(false), rows = ref([]), detail = ref(null), visible = ref(false)
const labels = {running: '分析中', success: '已推荐', no_recommendation: '空仓', failed: '失败', skipped_non_trading_day: '非交易日', missed_window: '错过交易窗口'}
const emailLabels = {pending: '待发送', sending: '发送中', retry_wait: '等待重试', sent: '已发送', failed: '发送失败', cancelled: '已取消'}
const dateTime = value => value ? String(value).slice(0, 19).replace('T', ' ') : '--'
const coverage = value => value !== null && value !== undefined && Number.isFinite(Number(value)) ? `${Number(value).toFixed(1)}%` : '--'
const qualityLabel = value => value === null || value === undefined ? '未记录' : value ? '降级' : '完整'
const qualityType = value => value === null || value === undefined ? 'default' : value ? 'warning' : 'success'
const degradedReason = run => run?.degraded === null || run?.degraded === undefined ? '历史运行未记录证据质量' : run.degraded ? (run.failureReason || '辅助证据不完整，详见报告与证据审计') : '无'
const type = status => status === 'success' ? 'success' : status === 'failed' ? 'error' : status === 'running' ? 'warning' : 'info'
async function show(row) { visible.value = true; detail.value = null; try { detail.value = await GetResearch2Run(row.runId) } catch (error) { message.error(error?.message || String(error)) } }
const columns = [
  {title: '交易日', key: 'tradingDate', width: 110},
  {title: '计划时间', key: 'scheduledFor', width: 170, render: row => dateTime(row.scheduledFor)},
  {title: '实际启动', key: 'startedAt', width: 170, render: row => dateTime(row.startedAt)},
  {title: '窗口开始', key: 'evidenceWindowStartAt', width: 170, render: row => dateTime(row.evidenceWindowStartAt)},
  {title: '证据截止', key: 'evidenceCutoffAt', width: 170, render: row => dateTime(row.evidenceCutoffAt)},
  {title: '报告生成', key: 'generatedAt', width: 170, render: row => dateTime(row.generatedAt)},
  {title: '证据覆盖', key: 'evidenceCoveragePct', width: 105, render: row => coverage(row.evidenceCoveragePct)},
  {title: '证据质量', key: 'degraded', width: 100, render: row => h(NTag, {type: qualityType(row.degraded), bordered: false}, {default: () => qualityLabel(row.degraded)})},
  {title: '时效', key: 'onTime', width: 90, render: row => h(NTag, {type: row.onTime ? 'success' : 'warning', bordered: false}, {default: () => row.onTime ? '准时' : '迟到'})},
  {title: '状态', key: 'status', width: 120, render: row => h(NTag, {type: type(row.status), bordered: false}, {default: () => labels[row.status] || row.status})},
  {title: '模型', key: 'modelName', minWidth: 180, render: row => `${row.providerName || '--'} / ${row.modelName || '--'}`},
  {title: '推荐数', key: 'recommendationCount', width: 90},
  {title: '邮件', key: 'emailDeliveryStatus', width: 110, render: row => h(NTag, {type: row.emailDeliveryStatus === 'sent' ? 'success' : row.emailDeliveryStatus === 'failed' ? 'error' : 'info', bordered: false}, {default: () => emailLabels[row.emailDeliveryStatus] || '未排队'})},
  {title: '说明', key: 'failureReason', minWidth: 220, ellipsis: {tooltip: true}, render: row => row.failureReason || '--'},
  {title: '操作', key: 'action', width: 90, render: row => h(NButton, {size: 'small', tertiary: true, type: 'primary', onClick: () => show(row)}, {default: () => '查看'})},
]
async function refresh() { loading.value = true; try { rows.value = await ListResearch2Runs() } catch (error) { message.error(error?.message || String(error)) } finally { loading.value = false } }
onMounted(refresh)
</script>

<template>
  <n-space vertical>
    <n-alert type="info" :bordered="false">自动任务计划 09:50 启动；以实际启动前5个已闭合交易分钟为证据窗口，报告校验完成后再获取可成交行情。</n-alert>
    <n-flex justify="end"><n-button :loading="loading" @click="refresh">刷新</n-button></n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="2130" :row-key="row => row.runId"/>
  </n-space>
  <n-modal v-model:show="visible">
    <n-card title="隔夜强势分析报告" closable style="width:min(1380px,96vw);max-height:94vh" @close="visible=false">
      <n-scrollbar style="max-height:82vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <ResearchAuditPanel owner-type="research2" :owner-id="String(detail.runId)" :active="visible">
              <template #final-result>
                <n-descriptions bordered :column="3" style="margin-bottom:12px">
                  <n-descriptions-item label="计划时间">{{dateTime(detail.scheduledFor)}}</n-descriptions-item>
                  <n-descriptions-item label="实际启动">{{dateTime(detail.startedAt)}}</n-descriptions-item>
                  <n-descriptions-item label="报告生成">{{dateTime(detail.generatedAt)}}</n-descriptions-item>
                  <n-descriptions-item label="窗口开始">{{dateTime(detail.evidenceWindowStartAt)}}</n-descriptions-item>
                  <n-descriptions-item label="证据截止">{{dateTime(detail.evidenceCutoffAt)}}</n-descriptions-item>
                  <n-descriptions-item label="证据覆盖">{{coverage(detail.evidenceCoveragePct)}}</n-descriptions-item>
                  <n-descriptions-item label="证据质量" :span="3">
                    <n-tag :type="qualityType(detail.degraded)" :bordered="false">{{qualityLabel(detail.degraded)}}</n-tag>
                    <span style="margin-left:8px">{{degradedReason(detail)}}</span>
                  </n-descriptions-item>
                </n-descriptions>
                <n-alert type="info" :show-icon="false" style="margin-bottom:12px">
                  邮件：{{ emailLabels[detail.emailDeliveryStatus] || '未排队' }}；尝试 {{ detail.emailAttemptCount || 0 }} 次；发送时间 {{ dateTime(detail.emailSentAt) }}
                  <template v-if="detail.emailLastError">；错误：{{ detail.emailLastError }}</template>
                </n-alert>
                <AppMarkdownPreview :model-value="detail.reportMarkdown || detail.failureReason || '暂无报告'"/>
              </template>
            </ResearchAuditPanel>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>
