<script setup>
import {computed, onBeforeUnmount, ref, watch} from 'vue'
import {useMessage} from 'naive-ui'
import {
  CreateResearchReplay,
  ExportResearchRunAudit,
  GetResearchReplay,
  GetResearchRunAudit,
  ListResearchReplayModelConfigs,
} from '../../services/research-audit-api'
import {
  AUDIT_TABS,
  auditIsAvailable,
  auditIsLegacy,
  auditPayloadLabel,
  modelConfigOptions,
  normalizeResearchAudit,
  prettyAuditValue,
  replayDifference,
  replayError,
  replayIsTerminal,
  replayStatus,
} from './audit-model.js'

const props = defineProps({
  ownerType: {
    type: String,
    required: true,
    validator: value => ['research1', 'research2'].includes(value),
  },
  ownerId: {type: String, required: true},
  active: {type: Boolean, default: true},
})

const message = useMessage()
const activeTab = ref(AUDIT_TABS[0].name)
const audit = ref(normalizeResearchAudit(null))
const loading = ref(false)
const loadError = ref('')
const exporting = ref(false)
const modelConfigsLoading = ref(false)
const modelConfigError = ref('')
const modelOptions = ref([])
const modelConfigId = ref(null)
const replayCreating = ref(false)
const replayRefreshing = ref(false)
const replayPollError = ref('')
const replay = ref(null)
let auditRequestVersion = 0
let replayRequestVersion = 0
let auditTimer = null
let replayTimer = null

const payloads = computed(() => audit.value.payloads || [])
const available = computed(() => auditIsAvailable(audit.value))
const legacy = computed(() => auditIsLegacy(audit.value))
const replayReady = computed(() => available.value && ['complete', 'completed'].includes(audit.value.state.status))
const currentReplayStatus = computed(() => replayStatus(replay.value))
const currentReplayDifference = computed(() => replayDifference(replay.value))
const currentReplayError = computed(() => replayError(replay.value))

function dateTime(value) {
  return value ? String(value).slice(0, 19).replace('T', ' ') : '--'
}

function statusType(status) {
  if (['complete', 'completed', 'success', 'available'].includes(status)) return 'success'
  if (['failed', 'error'].includes(status)) return 'error'
  if (['running', 'pending', 'queued'].includes(status)) return 'warning'
  return 'info'
}

function availabilityLabel(value) {
  if (value === 'available') return '审计可用'
  if (value === 'legacy_unavailable') return 'legacy_unavailable'
  return value || 'unavailable'
}

function stopAuditPolling() {
  if (auditTimer !== null) {
    clearTimeout(auditTimer)
    auditTimer = null
  }
}

function scheduleAuditPolling() {
  stopAuditPolling()
  if (!props.active || !props.ownerId || audit.value.state.status !== 'capturing') return
  auditTimer = setTimeout(refreshAudit, 2000)
}

function stopReplayPolling() {
  if (replayTimer !== null) {
    clearTimeout(replayTimer)
    replayTimer = null
  }
}

function resetReplay() {
  replayRequestVersion++
  stopReplayPolling()
  replay.value = null
  replayCreating.value = false
  replayRefreshing.value = false
  replayPollError.value = ''
}

async function loadModelConfigs() {
  if (modelOptions.value.length || modelConfigsLoading.value) return
  modelConfigsLoading.value = true
  modelConfigError.value = ''
  try {
    modelOptions.value = modelConfigOptions(await ListResearchReplayModelConfigs())
    if (!modelConfigId.value && modelOptions.value.length) modelConfigId.value = modelOptions.value[0].value
  } catch (error) {
    modelConfigError.value = error?.message || String(error)
  } finally {
    modelConfigsLoading.value = false
  }
}

async function loadAudit() {
  const requestVersion = ++auditRequestVersion
  stopAuditPolling()
  loadError.value = ''
  loading.value = false
  audit.value = normalizeResearchAudit(null, {ownerType: props.ownerType, ownerId: props.ownerId})
  activeTab.value = AUDIT_TABS[0].name
  resetReplay()
  if (!props.active || !props.ownerId) return
  loading.value = true
  try {
    const response = await GetResearchRunAudit(props.ownerType, props.ownerId)
    if (requestVersion !== auditRequestVersion) return
    audit.value = normalizeResearchAudit(response, {ownerType: props.ownerType, ownerId: props.ownerId})
    if (auditIsAvailable(audit.value)) void loadModelConfigs()
  } catch (error) {
    if (requestVersion === auditRequestVersion) loadError.value = error?.message || String(error)
  } finally {
    if (requestVersion === auditRequestVersion) {
      loading.value = false
      scheduleAuditPolling()
    }
  }
}

async function refreshAudit() {
  const requestVersion = auditRequestVersion
  try {
    const response = await GetResearchRunAudit(props.ownerType, props.ownerId)
    if (requestVersion !== auditRequestVersion) return
    audit.value = normalizeResearchAudit(response, {ownerType: props.ownerType, ownerId: props.ownerId})
    loadError.value = ''
    if (auditIsAvailable(audit.value)) void loadModelConfigs()
  } catch (error) {
    if (requestVersion === auditRequestVersion) loadError.value = error?.message || String(error)
  } finally {
    if (requestVersion === auditRequestVersion) scheduleAuditPolling()
  }
}

async function exportAudit() {
  if (!available.value || exporting.value) return
  exporting.value = true
  try {
    const filename = await ExportResearchRunAudit(props.ownerType, props.ownerId)
    message.success(`已下载脱敏审计包：${filename}`)
  } catch (error) {
    message.error(error?.message || String(error))
  } finally {
    exporting.value = false
  }
}

function scheduleReplayPolling() {
  stopReplayPolling()
  if (!props.active || !replay.value?.replayId || replayIsTerminal(replay.value)) return
  replayTimer = setTimeout(refreshReplay, 2000)
}

async function refreshReplay() {
  if (!replay.value?.replayId || replayRefreshing.value) return
  const requestVersion = replayRequestVersion
  const replayId = String(replay.value.replayId)
  replayRefreshing.value = true
  try {
    const refreshed = await GetResearchReplay(replayId)
    if (requestVersion !== replayRequestVersion) return
    replay.value = refreshed
    replayPollError.value = ''
  } catch (error) {
    if (requestVersion === replayRequestVersion) replayPollError.value = error?.message || String(error)
  } finally {
    if (requestVersion === replayRequestVersion) {
      replayRefreshing.value = false
      scheduleReplayPolling()
    }
  }
}

async function createReplay() {
  if (!replayReady.value || !modelConfigId.value || replayCreating.value) return
  resetReplay()
  const requestVersion = replayRequestVersion
  replayCreating.value = true
  try {
    const created = await CreateResearchReplay(props.ownerType, props.ownerId, Number(modelConfigId.value))
    if (requestVersion !== replayRequestVersion) return
    if (!created?.replayId) throw new Error('重放服务未返回 replayId')
    replay.value = created
    message.success('对照重放已创建')
    scheduleReplayPolling()
  } catch (error) {
    if (requestVersion === replayRequestVersion) message.error(error?.message || String(error))
  } finally {
    if (requestVersion === replayRequestVersion) replayCreating.value = false
  }
}

watch(() => [props.ownerType, props.ownerId, props.active], loadAudit, {immediate: true})
onBeforeUnmount(() => {
  auditRequestVersion++
  replayRequestVersion++
  stopAuditPolling()
  stopReplayPolling()
})
</script>

<template>
  <n-space vertical size="large" class="research-audit-panel">
    <n-flex justify="space-between" align="center" :wrap="true">
      <n-space align="center" :wrap="true">
        <n-tag :type="available ? 'success' : legacy ? 'warning' : 'default'" :bordered="false">
          {{ availabilityLabel(audit.availability) }}
        </n-tag>
        <n-text depth="3">证据截止：{{ dateTime(audit.cutoffAt) }}</n-text>
        <n-text v-if="audit.state.status" depth="3">审计状态：{{ audit.state.status }}</n-text>
        <n-text v-if="available" depth="3">载荷：{{ audit.state.payloadCount || payloads.length }}</n-text>
      </n-space>
      <n-button secondary type="primary" :disabled="!available" :loading="exporting" @click="exportAudit">
        下载脱敏审计包
      </n-button>
    </n-flex>

    <n-alert v-if="legacy" type="warning" title="legacy_unavailable">
      该运行创建于审计功能启用之前，系统不会伪造或重建当时的提示词、证据、模型调用和原始响应。
    </n-alert>
    <n-alert v-else-if="loadError" type="error" title="审计载入失败">{{ loadError }}</n-alert>
    <n-alert v-else-if="audit.state.lastError" type="error" title="审计运行错误">{{ audit.state.lastError }}</n-alert>

    <n-tabs v-model:value="activeTab" type="line" animated display-directive="if">
      <n-tab-pane name="final" tab="最终结果">
        <slot name="final-result"/>

        <n-divider title-placement="left">对照重放</n-divider>
        <n-alert type="info" :bordered="false">
          对照重放只写入独立重放记录，用于固定原证据、截止时间和提示词版本后的模型差异比较；不会改写正式推荐、交易、持仓或账户。
        </n-alert>
        <template v-if="available">
          <n-alert v-if="!replayReady" type="warning" :bordered="false" style="margin-top: 12px">
            审计状态尚未完成，当前不可创建对照重放。
          </n-alert>
          <n-flex align="center" :wrap="true" style="margin-top: 12px">
            <n-select
              v-model:value="modelConfigId"
              :options="modelOptions"
              :loading="modelConfigsLoading"
              placeholder="选择另一模型配置"
              style="width: min(420px, 100%)"
            />
            <n-button type="primary" :disabled="!modelConfigId || !replayReady" :loading="replayCreating" @click="createReplay">
              创建对照重放
            </n-button>
            <n-text v-if="modelConfigError" type="error">模型配置载入失败：{{ modelConfigError }}</n-text>
          </n-flex>
          <n-card v-if="replay" size="small" title="只读重放结果" style="margin-top: 12px">
            <n-descriptions bordered :column="3" size="small">
              <n-descriptions-item label="重放编号">{{ replay.replayId }}</n-descriptions-item>
              <n-descriptions-item label="状态">
                <n-tag :type="statusType(currentReplayStatus)" :bordered="false">{{ currentReplayStatus }}</n-tag>
              </n-descriptions-item>
              <n-descriptions-item label="模型配置">{{ replay.modelConfigId || modelConfigId }}</n-descriptions-item>
            </n-descriptions>
            <n-alert v-if="currentReplayError" type="error" style="margin-top: 10px">{{ currentReplayError }}</n-alert>
            <n-alert v-else-if="replayPollError" type="error" style="margin-top: 10px">重放状态刷新失败：{{ replayPollError }}</n-alert>
            <n-divider title-placement="left">重放结果</n-divider>
            <pre class="audit-code">{{ prettyAuditValue(replay.result, replayIsTerminal(replay) ? '未返回重放结果' : '重放运行中') }}</pre>
            <n-divider title-placement="left">结果差异</n-divider>
            <pre class="audit-code">{{ prettyAuditValue(currentReplayDifference, replayIsTerminal(replay) ? '未返回差异内容' : '重放运行中，完成后显示差异') }}</pre>
          </n-card>
        </template>
        <n-empty v-else description="当前运行没有可用于对照重放的审计快照" style="margin-top: 16px"/>
      </n-tab-pane>

      <n-tab-pane name="prompt" tab="提示词与输入">
        <n-spin :show="loading">
          <n-empty v-if="!available || !payloads.length" :description="legacy ? 'legacy_unavailable：旧运行无提示词记录' : '暂无提示词审计载荷'"/>
          <n-collapse v-else accordion>
            <n-collapse-item v-for="(payload, index) in payloads" :key="payload.payloadId" :name="payload.payloadId" :title="auditPayloadLabel(payload, index)">
              <n-descriptions bordered :column="2" size="small">
                <n-descriptions-item label="提示词 SHA-256"><n-text code>{{ payload.finalPromptSha256 || '--' }}</n-text></n-descriptions-item>
                <n-descriptions-item label="脱敏项数">{{ payload.redactionCount }}</n-descriptions-item>
                <n-descriptions-item label="模型参数" :span="2"><pre class="audit-code compact">{{ prettyAuditValue(payload.modelParameters) }}</pre></n-descriptions-item>
              </n-descriptions>
              <n-divider title-placement="left">最终发送内容</n-divider>
              <pre class="audit-code audit-prompt">{{ prettyAuditValue(payload.finalPrompt) }}</pre>
            </n-collapse-item>
          </n-collapse>
        </n-spin>
      </n-tab-pane>

      <n-tab-pane name="evidence" tab="证据快照">
        <n-spin :show="loading">
          <n-empty v-if="!available || !payloads.length" :description="legacy ? 'legacy_unavailable：旧运行无证据快照' : '暂无证据快照'"/>
          <n-collapse v-else accordion>
            <n-collapse-item v-for="(payload, index) in payloads" :key="payload.payloadId" :name="payload.payloadId" :title="auditPayloadLabel(payload, index)">
              <n-text depth="3">证据 SHA-256：</n-text><n-text code>{{ payload.evidenceSha256 || '--' }}</n-text>
              <pre class="audit-code">{{ prettyAuditValue(payload.evidenceSnapshot) }}</pre>
            </n-collapse-item>
          </n-collapse>
        </n-spin>
      </n-tab-pane>

      <n-tab-pane name="calls" tab="模型调用">
        <n-spin :show="loading">
          <n-empty v-if="!available || !payloads.length" :description="legacy ? 'legacy_unavailable：旧运行无模型调用记录' : '暂无模型调用记录'"/>
          <n-collapse v-else accordion>
            <n-collapse-item v-for="(payload, index) in payloads" :key="payload.payloadId" :name="payload.payloadId" :title="auditPayloadLabel(payload, index)">
              <n-descriptions bordered :column="3" size="small">
                <n-descriptions-item label="阶段">{{ payload.phase }}</n-descriptions-item>
                <n-descriptions-item label="调用 / 尝试">{{ payload.callSequence }} / {{ payload.attempt }}</n-descriptions-item>
                <n-descriptions-item label="创建时间">{{ dateTime(payload.createdAt) }}</n-descriptions-item>
                <n-descriptions-item label="Provider / 模型" :span="3">{{ payload.providerName || '--' }} / {{ payload.modelName || '--' }}</n-descriptions-item>
                <n-descriptions-item label="模型参数" :span="3"><pre class="audit-code compact">{{ prettyAuditValue(payload.modelParameters) }}</pre></n-descriptions-item>
                <n-descriptions-item label="工具清单" :span="3"><pre class="audit-code compact">{{ prettyAuditValue(payload.tools, '无工具调用') }}</pre></n-descriptions-item>
              </n-descriptions>
            </n-collapse-item>
          </n-collapse>
        </n-spin>
      </n-tab-pane>

      <n-tab-pane name="response" tab="原始响应与修复">
        <n-spin :show="loading">
          <n-empty v-if="!available || !payloads.length" :description="legacy ? 'legacy_unavailable：旧运行无原始响应' : '暂无响应与修复记录'"/>
          <n-collapse v-else accordion>
            <n-collapse-item v-for="(payload, index) in payloads" :key="payload.payloadId" :name="payload.payloadId" :title="auditPayloadLabel(payload, index)">
              <n-text depth="3">原始响应 SHA-256：</n-text><n-text code>{{ payload.rawResponseSha256 || '--' }}</n-text>
              <pre class="audit-code">{{ prettyAuditValue(payload.rawResponse) }}</pre>
              <n-divider title-placement="left">修复后响应</n-divider>
              <n-text depth="3">修复响应 SHA-256：</n-text><n-text code>{{ payload.repairedResponseSha256 || '--' }}</n-text>
              <pre class="audit-code">{{ prettyAuditValue(payload.repairedResponse, '未执行响应修复') }}</pre>
              <n-divider title-placement="left">修复日志</n-divider>
              <pre class="audit-code compact">{{ prettyAuditValue(payload.repairLog, '无修复日志') }}</pre>
            </n-collapse-item>
          </n-collapse>
        </n-spin>
      </n-tab-pane>
    </n-tabs>
  </n-space>
</template>

<style scoped>
.research-audit-panel {
  min-width: 0;
}

.audit-code {
  box-sizing: border-box;
  width: 100%;
  max-height: 440px;
  margin: 10px 0 0;
  padding: 12px;
  overflow: auto;
  border: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.25));
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.08);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.audit-code.compact {
  max-height: 220px;
  margin-top: 0;
}

.audit-prompt {
  max-height: 560px;
}
</style>
