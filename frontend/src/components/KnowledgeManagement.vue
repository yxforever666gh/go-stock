<script setup>
import {computed, h, onBeforeUnmount, onMounted, ref} from 'vue'
import {NButton, NSpace, NTag, useMessage} from 'naive-ui'
import {
  CreateKnowledgeFromResearch,
  DecideKnowledgeMemoryCandidate,
  DecideKnowledgeVersion,
  GetKnowledgeDocument,
  ListKnowledgeDocuments,
  ListKnowledgeDocumentVersions,
  ListKnowledgeMemoryCandidates,
  SearchKnowledge,
  UploadKnowledgeDocument,
  UploadKnowledgeDocumentVersion,
} from '../services/knowledge-api'
import {
  KNOWLEDGE_STATUS_OPTIONS,
  MEMORY_STATUS_OPTIONS,
  knowledgeOriginLabel,
  knowledgeSourceLabel,
  knowledgeStatusLabel,
  knowledgeStatusMeaning,
  knowledgeStatusType,
  memoryStatusLabel,
  memoryStatusMeaning,
  normalizeKnowledgeDocument,
  normalizeKnowledgeDocumentPage,
  normalizeKnowledgeSearchHits,
  normalizeMemoryCandidate,
  normalizeMemoryCandidates,
} from './knowledge/knowledge-model.js'

const message = useMessage()
const activeTab = ref('documents')
const documentLoading = ref(false)
const documentPage = ref({items: [], total: 0, page: 1, pageSize: 20})
const documentFilters = ref({status: '', q: ''})
const uploadTitle = ref('')
const uploadingDocument = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref(null)
const uploadingVersion = ref(false)
const searchQuery = ref('')
const searchLoading = ref(false)
const searchHits = ref([])
const memoryStatus = ref('draft')
const memoryLoading = ref(false)
const memoryCandidates = ref([])
const candidateVisible = ref(false)
const selectedCandidate = ref(null)
const decisionVisible = ref(false)
const decisionTarget = ref(null)
const decisionReason = ref('')
const decisionLoading = ref(false)
const researchDraft = ref({sourceOwnerType: 'research1', sourceOwnerId: '', title: ''})
const researchDraftLoading = ref(false)
let documentRequestVersion = 0
let detailRequestVersion = 0
let searchRequestVersion = 0
let memoryRequestVersion = 0

const versions = computed(() => detail.value?.versions || [])
const detailDrawerWidth = computed(() => {
  if (typeof window === 'undefined') return 920
  return Math.min(920, Math.max(360, window.innerWidth - 40))
})
const decisionIsReject = computed(() => decisionTarget.value?.decision === 'rejected')
const decisionTitle = computed(() => {
  const target = decisionTarget.value
  if (!target) return '知识审批'
  const action = target.decision === 'approved' ? '批准' : '拒绝'
  return `${action}${target.kind === 'memory' ? '记忆候选' : '知识版本'}：${target.title}`
})

function dateTime(value) {
  return value ? String(value).slice(0, 19).replace('T', ' ') : '--'
}

function shortHash(value) {
  const text = String(value || '')
  return text ? `${text.slice(0, 12)}${text.length > 12 ? '…' : ''}` : '--'
}

function versionLabel(version) {
  return version?.versionNo ? `v${version.versionNo}` : version?.versionId || '--'
}

const documentColumns = [
  {title: '标题', key: 'title', minWidth: 210, ellipsis: {tooltip: true}},
  {title: '最新版本', key: 'version', width: 105, render: row => row.latestVersionNumber ? `v${row.latestVersionNumber}` : versionLabel(row.latestVersion)},
  {title: '状态', key: 'status', width: 105, render: row => h(NTag, {type: knowledgeStatusType(row.status), bordered: false}, {default: () => knowledgeStatusLabel(row.status)})},
  {title: '来源', key: 'source', minWidth: 190, ellipsis: {tooltip: true}, render: row => row.latestVersion?.sourceFilename || knowledgeOriginLabel(row)},
  {title: '内容 SHA-256', key: 'hash', width: 150, render: row => shortHash(row.latestVersion?.contentSha256)},
  {title: '更新时间', key: 'updatedAt', width: 170, render: row => dateTime(row.updatedAt || row.latestVersion?.updatedAt)},
  {title: '操作', key: 'action', width: 100, render: row => h(NButton, {size: 'small', tertiary: true, type: 'primary', onClick: () => showDocument(row.documentId)}, {default: () => '详情'})},
]

const searchColumns = [
  {title: '知识文档', key: 'title', minWidth: 180, ellipsis: {tooltip: true}},
  {title: '相关度', key: 'score', width: 95, render: row => Number(row.score || 0).toFixed(4)},
  {title: '摘要命中', key: 'snippet', minWidth: 320, ellipsis: {tooltip: true}},
  {title: '来源', key: 'source', minWidth: 180, ellipsis: {tooltip: true}, render: row => knowledgeSourceLabel(row.sourceOwnerType, row.sourceOwnerId)},
  {title: '状态', key: 'status', width: 100, render: row => h(NTag, {type: knowledgeStatusType(row.status), bordered: false}, {default: () => knowledgeStatusLabel(row.status)})},
  {title: '内容 SHA-256', key: 'hash', width: 150, render: row => shortHash(row.contentSha256)},
  {title: '操作', key: 'action', width: 90, render: row => h(NButton, {size: 'small', tertiary: true, onClick: () => showDocument(row.documentId)}, {default: () => '查看'})},
]

const memoryColumns = [
  {title: '标题', key: 'title', minWidth: 200, ellipsis: {tooltip: true}},
  {title: '来源运行', key: 'source', minWidth: 220, render: row => knowledgeSourceLabel(row.sourceOwnerType, row.sourceOwnerId)},
  {title: '状态', key: 'status', width: 105, render: row => h(NTag, {type: knowledgeStatusType(row.status), bordered: false}, {default: () => memoryStatusLabel(row.status)})},
  {title: '内容 SHA-256', key: 'hash', width: 150, render: row => shortHash(row.contentSha256)},
  {title: '提出者', key: 'proposer', minWidth: 130, render: row => row.proposedByActorType || '--'},
  {title: '更新时间', key: 'updatedAt', width: 170, render: row => dateTime(row.updatedAt || row.createdAt)},
  {title: '操作', key: 'action', width: 230, render: row => h(NSpace, {size: 6}, {default: () => [
    h(NButton, {size: 'small', tertiary: true, onClick: () => showCandidate(row)}, {default: () => '详情'}),
    row.status === 'draft' ? h(NButton, {size: 'small', type: 'success', secondary: true, onClick: () => openDecision('memory', row, 'approved')}, {default: () => '批准'}) : null,
    row.status === 'draft' ? h(NButton, {size: 'small', type: 'error', secondary: true, onClick: () => openDecision('memory', row, 'rejected')}, {default: () => '拒绝'}) : null,
  ]})},
]

async function loadDocuments(page = documentPage.value.page) {
  const requestVersion = ++documentRequestVersion
  documentLoading.value = true
  try {
    const response = await ListKnowledgeDocuments({
      page,
      pageSize: documentPage.value.pageSize,
      status: documentFilters.value.status,
      q: documentFilters.value.q,
    })
    if (requestVersion !== documentRequestVersion) return
    documentPage.value = normalizeKnowledgeDocumentPage(response)
  } catch (error) {
    if (requestVersion === documentRequestVersion) message.error(error?.message || String(error))
  } finally {
    if (requestVersion === documentRequestVersion) documentLoading.value = false
  }
}

function applyDocumentFilters() {
  documentPage.value.page = 1
  loadDocuments(1)
}

async function showDocument(documentId) {
  if (!documentId) return
  const requestVersion = ++detailRequestVersion
  detailVisible.value = true
  detailLoading.value = true
  detail.value = null
  try {
    const [documentResponse, versionResponse] = await Promise.all([
      GetKnowledgeDocument(documentId),
      ListKnowledgeDocumentVersions(documentId),
    ])
    if (requestVersion !== detailRequestVersion) return
    const normalized = normalizeKnowledgeDocument(documentResponse)
    detail.value = normalizeKnowledgeDocument({...normalized, versions: versionResponse})
  } catch (error) {
    if (requestVersion === detailRequestVersion) message.error(error?.message || String(error))
  } finally {
    if (requestVersion === detailRequestVersion) detailLoading.value = false
  }
}

async function handleDocumentUpload({file, onFinish, onError}) {
  uploadingDocument.value = true
  try {
    await UploadKnowledgeDocument(file.file || file, uploadTitle.value)
    uploadTitle.value = ''
    onFinish?.()
    message.success('知识文档已导入为草稿，批准前不会进入研究检索')
    await loadDocuments(1)
  } catch (error) {
    onError?.()
    message.error(error?.message || String(error))
  } finally {
    uploadingDocument.value = false
  }
}

async function handleVersionUpload({file, onFinish, onError}) {
  if (!detail.value?.documentId) return
  uploadingVersion.value = true
  try {
    await UploadKnowledgeDocumentVersion(detail.value.documentId, file.file || file)
    onFinish?.()
    message.success('新版本已保存为草稿；批准后会替代旧的已批准版本')
    await Promise.all([showDocument(detail.value.documentId), loadDocuments()])
  } catch (error) {
    onError?.()
    message.error(error?.message || String(error))
  } finally {
    uploadingVersion.value = false
  }
}

async function runSearch() {
  const query = searchQuery.value.trim()
  if (!query) {
    searchHits.value = []
    message.warning('请输入全文检索关键词')
    return
  }
  const requestVersion = ++searchRequestVersion
  searchLoading.value = true
  searchHits.value = []
  try {
    const response = await SearchKnowledge(query, 20)
    if (requestVersion === searchRequestVersion) searchHits.value = normalizeKnowledgeSearchHits(response)
  } catch (error) {
    if (requestVersion === searchRequestVersion) message.error(error?.message || String(error))
  } finally {
    if (requestVersion === searchRequestVersion) searchLoading.value = false
  }
}

async function loadMemoryCandidates() {
  const requestVersion = ++memoryRequestVersion
  memoryLoading.value = true
  try {
    const response = await ListKnowledgeMemoryCandidates(memoryStatus.value || undefined)
    if (requestVersion === memoryRequestVersion) memoryCandidates.value = normalizeMemoryCandidates(response)
  } catch (error) {
    if (requestVersion === memoryRequestVersion) message.error(error?.message || String(error))
  } finally {
    if (requestVersion === memoryRequestVersion) memoryLoading.value = false
  }
}

function showCandidate(candidate) {
  selectedCandidate.value = normalizeMemoryCandidate(candidate)
  candidateVisible.value = true
}

function openDecision(kind, target, decision) {
  decisionTarget.value = {
    kind,
    decision,
    id: kind === 'memory' ? target.candidateId : target.versionId,
    title: kind === 'memory' ? target.title : versionLabel(target),
  }
  decisionReason.value = ''
  decisionVisible.value = true
}

async function confirmDecision() {
  const target = decisionTarget.value
  if (!target?.id || decisionLoading.value) return
  if (target.decision === 'rejected' && !decisionReason.value.trim()) {
    message.warning('拒绝时请填写原因')
    return
  }
  decisionLoading.value = true
  try {
    if (target.kind === 'memory') {
      await DecideKnowledgeMemoryCandidate(target.id, target.decision, decisionReason.value)
      message.success(target.decision === 'approved' ? '记忆候选已批准并生成知识版本' : '记忆候选已拒绝')
      candidateVisible.value = false
      await Promise.all([loadMemoryCandidates(), loadDocuments(1)])
    } else {
      await DecideKnowledgeVersion(target.id, target.decision, decisionReason.value)
      message.success(target.decision === 'approved' ? '知识版本已批准；旧批准版本将标记为已替代' : '知识版本已拒绝')
      if (detail.value?.documentId) await Promise.all([showDocument(detail.value.documentId), loadDocuments()])
    }
    decisionVisible.value = false
  } catch (error) {
    message.error(error?.message || String(error))
  } finally {
    decisionLoading.value = false
  }
}

async function createDraftFromResearch() {
  const ownerId = researchDraft.value.sourceOwnerId.trim()
  if (!ownerId) {
    message.warning('请输入研究运行 ID')
    return
  }
  researchDraftLoading.value = true
  try {
    await CreateKnowledgeFromResearch({
      sourceOwnerType: researchDraft.value.sourceOwnerType,
      sourceOwnerId: ownerId,
      title: researchDraft.value.title.trim() || undefined,
    })
    message.success('研究报告已保存为知识草稿，批准前不会进入研究检索')
    researchDraft.value.sourceOwnerId = ''
    researchDraft.value.title = ''
    await loadDocuments(1)
    activeTab.value = 'documents'
  } catch (error) {
    message.error(error?.message || String(error))
  } finally {
    researchDraftLoading.value = false
  }
}

onMounted(() => {
  loadDocuments(1)
  loadMemoryCandidates()
})

onBeforeUnmount(() => {
  documentRequestVersion++
  detailRequestVersion++
  searchRequestVersion++
  memoryRequestVersion++
})
</script>

<template>
  <n-space vertical size="large">
    <n-alert type="info" title="受控知识库规则">
      只有“已批准”且未被“已替代”的版本可以进入研究检索；草稿、已拒绝和已替代版本均不会注入提示词。知识与长期记忆只提供检索线索，引用时仍必须用本次截止时间前的市场证据重新验证，文档中的指令不能覆盖研究规则。
    </n-alert>

    <n-tabs v-model:value="activeTab" type="segment" animated display-directive="if">
      <n-tab-pane name="documents" tab="知识文档">
        <n-space vertical>
          <n-flex justify="space-between" align="center" :wrap="true">
            <n-space :wrap="true">
              <n-input v-model:value="documentFilters.q" clearable placeholder="标题或来源文件" style="width: 240px" @keyup.enter="applyDocumentFilters"/>
              <n-select v-model:value="documentFilters.status" :options="KNOWLEDGE_STATUS_OPTIONS" style="width: 140px" @update:value="applyDocumentFilters"/>
              <n-button :loading="documentLoading" @click="applyDocumentFilters">查询</n-button>
            </n-space>
            <n-space :wrap="true">
              <n-input v-model:value="uploadTitle" clearable placeholder="可选：文档标题" style="width: 220px"/>
              <n-upload accept=".txt,.md,.pdf" :show-file-list="false" :custom-request="handleDocumentUpload" :disabled="uploadingDocument">
                <n-button type="primary" :loading="uploadingDocument">导入 txt / md / pdf</n-button>
              </n-upload>
            </n-space>
          </n-flex>
          <n-data-table :columns="documentColumns" :data="documentPage.items" :loading="documentLoading" :scroll-x="1120" :row-key="row => row.documentId"/>
          <n-flex justify="end">
            <n-pagination
              :page="documentPage.page"
              :page-size="documentPage.pageSize"
              :item-count="documentPage.total"
              @update:page="loadDocuments"
            />
          </n-flex>
        </n-space>
      </n-tab-pane>

      <n-tab-pane name="search" tab="全文检索">
        <n-space vertical>
          <n-alert type="info" :bordered="false">检索仅命中已批准且未被替代的版本；命中内容仍是外部证据线索，不等于本次研究事实。</n-alert>
          <n-flex :wrap="true">
            <n-input v-model:value="searchQuery" clearable placeholder="输入全文关键词" style="width: min(560px, 100%)" @keyup.enter="runSearch"/>
            <n-button type="primary" :loading="searchLoading" @click="runSearch">检索</n-button>
          </n-flex>
          <n-data-table :columns="searchColumns" :data="searchHits" :loading="searchLoading" :scroll-x="1320" :row-key="row => row.retrievalHitId"/>
        </n-space>
      </n-tab-pane>

      <n-tab-pane name="memory" tab="记忆候选">
        <n-space vertical>
          <n-alert type="warning" :bordered="false">AI 只能提出记忆候选，不能自行批准。只有用户点击批准后才会生成知识版本。</n-alert>
          <n-flex justify="space-between" align="center">
            <n-select v-model:value="memoryStatus" :options="MEMORY_STATUS_OPTIONS" style="width: 150px" @update:value="loadMemoryCandidates"/>
            <n-button :loading="memoryLoading" @click="loadMemoryCandidates">刷新</n-button>
          </n-flex>
          <n-data-table :columns="memoryColumns" :data="memoryCandidates" :loading="memoryLoading" :scroll-x="1250" :row-key="row => row.candidateId"/>
        </n-space>
      </n-tab-pane>

      <n-tab-pane name="from-research" tab="研究报告转草稿">
        <n-card size="small" title="从既有研究运行创建知识草稿">
          <n-alert type="info" :bordered="false" style="margin-bottom: 14px">可选择研究中心1或研究中心2作为来源。创建结果始终是草稿，必须由用户另行批准。</n-alert>
          <n-form label-placement="left" label-width="120">
            <n-form-item label="来源研究中心">
              <n-radio-group v-model:value="researchDraft.sourceOwnerType">
                <n-radio-button value="research1">研究中心1</n-radio-button>
                <n-radio-button value="research2">研究中心2</n-radio-button>
              </n-radio-group>
            </n-form-item>
            <n-form-item label="研究运行 ID"><n-input v-model:value="researchDraft.sourceOwnerId" placeholder="请输入 runId"/></n-form-item>
            <n-form-item label="草稿标题"><n-input v-model:value="researchDraft.title" placeholder="可选；默认使用报告标题"/></n-form-item>
            <n-form-item><n-button type="primary" :loading="researchDraftLoading" @click="createDraftFromResearch">保存为知识草稿</n-button></n-form-item>
          </n-form>
        </n-card>
      </n-tab-pane>
    </n-tabs>
  </n-space>

  <n-drawer v-model:show="detailVisible" :width="detailDrawerWidth">
    <n-drawer-content title="知识文档详情" closable>
      <n-spin :show="detailLoading">
        <template v-if="detail">
          <n-descriptions bordered :column="2" size="small">
            <n-descriptions-item label="标题">{{ detail.title }}</n-descriptions-item>
            <n-descriptions-item label="文档 ID"><n-text code>{{ detail.documentId }}</n-text></n-descriptions-item>
            <n-descriptions-item label="创建时间">{{ dateTime(detail.createdAt) }}</n-descriptions-item>
            <n-descriptions-item label="更新时间">{{ dateTime(detail.updatedAt) }}</n-descriptions-item>
            <n-descriptions-item label="文档类型">{{ detail.documentType || '--' }}</n-descriptions-item>
            <n-descriptions-item label="来源">{{ knowledgeOriginLabel(detail) }}</n-descriptions-item>
            <n-descriptions-item label="标签" :span="2">{{ detail.tags.length ? detail.tags.join('、') : '--' }}</n-descriptions-item>
            <n-descriptions-item label="说明" :span="2">{{ detail.description || '--' }}</n-descriptions-item>
          </n-descriptions>
          <n-flex justify="space-between" align="center" style="margin-top: 14px">
            <n-text strong>不可变版本</n-text>
            <n-upload accept=".txt,.md,.pdf" :show-file-list="false" :custom-request="handleVersionUpload" :disabled="uploadingVersion">
              <n-button secondary type="primary" :loading="uploadingVersion">上传新版本草稿</n-button>
            </n-upload>
          </n-flex>
          <n-empty v-if="!versions.length" description="暂无版本" style="margin-top: 24px"/>
          <n-collapse v-else accordion style="margin-top: 12px">
            <n-collapse-item v-for="version in versions" :key="version.versionId" :name="version.versionId">
              <template #header>
                <n-space align="center">
                  <n-text strong>{{ versionLabel(version) }}</n-text>
                  <n-tag :type="knowledgeStatusType(version.status)" :bordered="false">{{ knowledgeStatusLabel(version.status) }}</n-tag>
                  <n-text depth="3">{{ version.sourceFilename || version.mimeType || '--' }}</n-text>
                </n-space>
              </template>
              <n-alert :type="version.status === 'approved' ? 'success' : version.status === 'rejected' ? 'error' : 'info'" :bordered="false">
                {{ knowledgeStatusMeaning(version.status) }}
              </n-alert>
              <n-descriptions bordered :column="2" size="small" style="margin-top: 10px">
                <n-descriptions-item label="版本 ID"><n-text code>{{ version.versionId }}</n-text></n-descriptions-item>
                <n-descriptions-item label="内容 SHA-256"><n-text code>{{ version.contentSha256 || '--' }}</n-text></n-descriptions-item>
                <n-descriptions-item label="MIME / 提取">{{ version.mimeType || '--' }} / {{ version.extractionStatus || '--' }}</n-descriptions-item>
                <n-descriptions-item label="创建时间">{{ dateTime(version.createdAt) }}</n-descriptions-item>
                <n-descriptions-item v-if="version.extractionError" label="提取错误" :span="2"><n-text type="error">{{ version.extractionError }}</n-text></n-descriptions-item>
                <n-descriptions-item v-if="version.decisionReason" label="审批原因" :span="2">{{ version.decisionReason }}</n-descriptions-item>
              </n-descriptions>
              <n-flex v-if="version.status === 'draft'" justify="end" style="margin-top: 10px">
                <n-button size="small" secondary type="success" @click="openDecision('version', version, 'approved')">批准版本</n-button>
                <n-button size="small" secondary type="error" @click="openDecision('version', version, 'rejected')">拒绝版本</n-button>
              </n-flex>
              <n-divider title-placement="left">提取文本</n-divider>
              <pre class="knowledge-content">{{ version.contentText || '暂无可提取文本' }}</pre>
            </n-collapse-item>
          </n-collapse>
        </template>
      </n-spin>
    </n-drawer-content>
  </n-drawer>

  <n-modal v-model:show="candidateVisible">
    <n-card v-if="selectedCandidate" title="记忆候选详情" closable style="width:min(860px,94vw);max-height:90vh" @close="candidateVisible=false">
      <n-scrollbar style="max-height:76vh">
        <n-descriptions bordered :column="2" size="small">
          <n-descriptions-item label="标题">{{ selectedCandidate.title }}</n-descriptions-item>
          <n-descriptions-item label="状态"><n-tag :type="knowledgeStatusType(selectedCandidate.status)" :bordered="false">{{ memoryStatusLabel(selectedCandidate.status) }}</n-tag></n-descriptions-item>
          <n-descriptions-item label="来源">{{ knowledgeSourceLabel(selectedCandidate.sourceOwnerType, selectedCandidate.sourceOwnerId) }}</n-descriptions-item>
          <n-descriptions-item label="内容 SHA-256"><n-text code>{{ selectedCandidate.contentSha256 || '--' }}</n-text></n-descriptions-item>
          <n-descriptions-item label="创建时间">{{ dateTime(selectedCandidate.createdAt) }}</n-descriptions-item>
          <n-descriptions-item label="更新时间">{{ dateTime(selectedCandidate.updatedAt) }}</n-descriptions-item>
          <n-descriptions-item v-if="selectedCandidate.decisionByUserId" label="审批人">{{ selectedCandidate.decisionByUserId }}</n-descriptions-item>
          <n-descriptions-item v-if="selectedCandidate.decidedAt" label="审批时间">{{ dateTime(selectedCandidate.decidedAt) }}</n-descriptions-item>
          <n-descriptions-item v-if="selectedCandidate.decisionReason" label="审批原因" :span="2">{{ selectedCandidate.decisionReason }}</n-descriptions-item>
          <n-descriptions-item v-if="selectedCandidate.approvedVersionId" label="批准版本" :span="2"><n-text code>{{ selectedCandidate.approvedVersionId }}</n-text></n-descriptions-item>
        </n-descriptions>
        <n-alert type="info" :bordered="false" style="margin-top: 10px">{{ memoryStatusMeaning(selectedCandidate.status) }}</n-alert>
        <pre class="knowledge-content">{{ selectedCandidate.contentText || '暂无候选内容' }}</pre>
        <n-flex v-if="selectedCandidate.status === 'draft'" justify="end">
          <n-button secondary type="success" @click="openDecision('memory', selectedCandidate, 'approved')">批准候选</n-button>
          <n-button secondary type="error" @click="openDecision('memory', selectedCandidate, 'rejected')">拒绝候选</n-button>
        </n-flex>
      </n-scrollbar>
    </n-card>
  </n-modal>

  <n-modal v-model:show="decisionVisible">
    <n-card :title="decisionTitle" closable style="width:min(620px,92vw)" @close="decisionVisible=false">
      <n-alert :type="decisionIsReject ? 'warning' : 'info'" :bordered="false" style="margin-bottom: 12px">
        <template v-if="decisionTarget?.kind === 'memory' && !decisionIsReject">批准后端将创建知识文档和版本；这是必须由用户发起的批准操作。</template>
        <template v-else-if="!decisionIsReject">批准新版本后，原已批准版本会变为“已替代”并退出研究检索。</template>
        <template v-else>拒绝对象不会进入研究检索，拒绝原因将被保留。</template>
      </n-alert>
      <n-input v-model:value="decisionReason" type="textarea" :rows="4" :placeholder="decisionIsReject ? '请填写拒绝原因' : '可选：填写批准说明'"/>
      <template #footer>
        <n-flex justify="end">
          <n-button @click="decisionVisible=false">取消</n-button>
          <n-button :type="decisionIsReject ? 'error' : 'success'" :loading="decisionLoading" @click="confirmDecision">确认{{ decisionIsReject ? '拒绝' : '批准' }}</n-button>
        </n-flex>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.knowledge-content {
  box-sizing: border-box;
  width: 100%;
  max-height: 420px;
  margin: 12px 0;
  padding: 12px;
  overflow: auto;
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.08);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
