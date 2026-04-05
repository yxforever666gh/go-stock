<script setup>
import {computed, h, onBeforeMount, onMounted, reactive, ref} from 'vue'
import {GetAIResponseResultList, GetConfig, SaveAsMarkdown, ShareAnalysis,DeleteAIResponseResult} from "../services/app-api";
import {NAvatar, NButton, NEllipsis, NText, useMessage} from "naive-ui";
import {MdEditor, MdPreview} from 'md-editor-v3';



onBeforeMount(()=> {
  GetConfig().then(result => {
    if (result.darkTheme) {
      editorDataRef.darkTheme = true
    }
  })
})
onMounted(() => {
  query({
    page: 1,
    pageSize: paginationReactive.pageSize,
    order: "desc",
    keyword: paginationReactive.keyword,
    startDate: formatDate(paginationReactive.range[0]),
    endDate: formatDate(paginationReactive.range[1])
  }).then((data) => {
    console.log( data)
    dataRef.value = data.data
    paginationReactive.page = 1
    paginationReactive.pageCount = data.pageCount
    paginationReactive.itemCount = data.total
    loadingRef.value = false
  })
})
const message = useMessage()
const mdPreviewRef = ref(null)
const mdEditorRef = ref(null)
const editorDataRef = reactive({
  show: false,
  loading: false,
  darkTheme: false,
  chatId: "",
  modelName: "",
  CreatedAt: "",
  stockName: "",
  stockCode: "",
  question: "",
  content: "",
})
const dataRef = ref([])
const loadingRef = ref(true)
const tableScrollX = 1100
const columnsRef = ref([
  {
    title: '分析时间',
    key: 'CreatedAt',
    render(row, index) {
      //2026-01-14T22:13:27.2693252+08:00 格式化为常用时间格式
      return row.CreatedAt.substring(0, 19).replace('T', ' ')
    }
  },
  {
    title: '模型名称',
    key: 'modelName'
  },
  {
    title: '分析对象',
    key: 'stockName'
  },
  {
    title: '提示词',
    key: 'question',
    render(row, index) {
      return h(NEllipsis, { tooltip: true ,style: "max-width: 240px;"}, {default: () => h(NText,{type: "info"},{default: () => row.question}),})
    }
  },
  {
    title: '操作',
    render(row, index) {
      return [h(
          NButton,
          {
            strong: true,
            tertiary: true,
            size: 'small',
            type: 'warning', // 橙色按钮
            style: 'font-size: 14px; padding: 0 10px;', // 稍微大一点的按钮
            onClick: () => showReport(row)
          },
          { default: () => '查看分析报告' }
      ),
      h(
          NButton,
          {
            strong: true,
            tertiary: true,
            size: 'small',
            type: 'error', // 橙色按钮
            style: 'font-size: 14px; padding: 0 10px;', // 稍微大一点的按钮
            onClick: () => deleteAIResponseResult(row.ID)
          },
          { default: () => '删除' }
      ),
      ]
    }
  },
])
const paginationReactive = reactive({
  page: 1,
  pageCount: 1,
  pageSize: 12,
  itemCount: 0,
  keyword: "",
  startDate:"",
  range: [
    new Date(new Date().getTime() - 3 * 24 * 60 * 60 * 1000), // 前3天
    new Date() // 当天
  ],
  prefix({ itemCount }) {
    return `${itemCount} 条记录`
  }
})
const theme = computed(() => {
  return editorDataRef.darkTheme ? 'dark' : 'light'
})
const reportTitle = computed(() => {
  const stockName = String(editorDataRef.stockName || "").trim()
  const stockCode = String(editorDataRef.stockCode || "").trim()
  if (stockName && stockCode) {
    return `${stockName} [${stockCode}] AI分析报告`
  }
  if (stockName) {
    return `${stockName} AI分析报告`
  }
  if (stockCode) {
    return `${stockCode} AI分析报告`
  }
  return "AI分析报告"
})

function showReport(row) {

  editorDataRef.show = true
  editorDataRef.chatId = row.chatId
  editorDataRef.modelName = row.modelName
  editorDataRef.CreatedAt = row.CreatedAt.substring(0, 19).replace('T', ' ')
  editorDataRef.stockName = row.stockName
  editorDataRef.stockCode = row.stockCode
  editorDataRef.question = row.question
  editorDataRef.content = row.content
  editorDataRef.loading = false
}

function query({
                 page,
                 pageSize = 10,
                 order = 'desc',
                 keyword = "",
                 startDate = "",
                 endDate = ""
               }) {
  return new Promise((resolve) => {

    GetAIResponseResultList({
      "page": page,
      "pageSize": pageSize,
      "modelName":keyword,
      "question":keyword,
      "stockName":keyword,
      "stockCode":keyword,
      "startDate":startDate,
      "endDate":endDate
    }).then((res) => {
      const pagedData =res.list
      const total = res.total
      const pageCount =res.totalPages
      resolve({
        pageCount,
        data: pagedData,
        total
      })
    })
  })
}

function handlePageChange(currentPage) {
  if (!loadingRef.value) {
    loadingRef.value = true
    query({
      page: currentPage,
      pageSize: paginationReactive.pageSize,
      order: "desc",
      keyword: paginationReactive.keyword,
      startDate: formatDate(paginationReactive.range[0]),
      endDate: formatDate(paginationReactive.range[1])
    }).then((data) => {
      dataRef.value = data.data
      paginationReactive.page = currentPage
      paginationReactive.pageCount = data.pageCount
      paginationReactive.itemCount = data.total
      loadingRef.value = false
    })
  }
}
function handleSearch() {
  if (!loadingRef.value) {
    loadingRef.value = true
    query({
      page: 1,
      pageSize: paginationReactive.pageSize,
      order: "desc",
      keyword: paginationReactive.keyword,
      startDate: formatDate(paginationReactive.range[0]),
      endDate: formatDate(paginationReactive.range[1])
    }).then((data) => {
      dataRef.value = data.data
      paginationReactive.page = 1
      paginationReactive.pageCount = data.pageCount
      paginationReactive.itemCount = data.total
      loadingRef.value = false
    })
  }
}
function share(code, name) {
  ShareAnalysis(code, name).then(msg => {
    //message.info(msg)
    notify.info({
      avatar: () =>
          h(NAvatar, {
            size: 'small',
            round: false,
            src: icon.value
          }),
      title: '分享到社区',
      duration: 1000 * 30,
      content: () => {
        return h('div', {
          style: {
            'text-align': 'left',
            'font-size': '14px',
          }
        }, {default: () => msg})
      },
    })
  })
}

function saveAsMarkdown(code,name) {
  SaveAsMarkdown(code, name).then(result => {
    if(result !== ""){
      message.success(result)
    }
  })
}
async function copyToClipboard() {
  try {
    await navigator.clipboard.writeText(editorDataRef.content);
    message.success('分析结果已复制到剪切板');
  } catch (err) {
    message.error('复制失败: ' + err);
  }
}
function formatDate(dateString) {
  const date = new Date(dateString)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  // const hours = String(date.getHours()).padStart(2, '0')
  // const minutes = String(date.getMinutes()).padStart(2, '0')
  // const seconds = String(date.getSeconds()).padStart(2, '0')
  //return `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
  return `${year}-${month}-${day}`
}

function deleteAIResponseResult(id){
  DeleteAIResponseResult(id).then(result => {
    if(result !== ""){
      message.success(result)
    }
    handleSearch()
  })
}
</script>

<template>
  <div class="report-toolbar">
    <n-date-picker v-model:value="paginationReactive.range" type="daterange" class="report-toolbar-date"/>
    <n-input clearable placeholder="输入关键词搜索" v-model:value="paginationReactive.keyword" class="report-toolbar-keyword"/>
    <n-button type="primary" ghost @click="handleSearch" @input="handleSearch" class="report-toolbar-button">
      搜索
    </n-button>
  </div>

  <n-data-table
      remote
      size="small"
      :columns="columnsRef"
      :data="dataRef"
      :loading="loadingRef"
      :pagination="paginationReactive"
      :scroll-x="tableScrollX"
      :row-key="(rowData)=>rowData.ID"
      @update:page="handlePageChange"
      flex-height
      class="report-table"
  />

  <n-modal
      transform-origin="center"
      v-model:show="editorDataRef.show"
      class="report-modal"
      :mask-closable="true"
  >
    <div class="report-modal-shell">
      <div class="report-modal-header">
        <div class="report-modal-heading">
          <div class="report-modal-title">{{ reportTitle }}</div>
          <div class="report-modal-meta">
            <n-tag v-if="editorDataRef.modelName" type="warning" round :bordered="false">
              {{ editorDataRef.modelName }}
            </n-tag>
            <span v-if="editorDataRef.CreatedAt">{{ editorDataRef.CreatedAt }}</span>
            <span v-if="editorDataRef.question" class="report-modal-question" :title="editorDataRef.question">
              {{ editorDataRef.question }}
            </span>
          </div>
        </div>
        <n-button quaternary @click="editorDataRef.show = false">关闭</n-button>
      </div>

      <n-spin size="small" :show="editorDataRef.loading" class="report-modal-spin">
        <div class="report-modal-body">
          <div class="report-preview-wrap">
            <MdPreview
                ref="mdPreviewRef"
                :modelValue="editorDataRef.content"
                :theme="theme"
                class="report-preview"
            />
          </div>
        </div>
      </n-spin>

      <div class="report-modal-footer">
        <div class="report-modal-note">
          *AI分析结果仅供参考，请以实际行情为准。投资需谨慎，风险自担。
        </div>
        <div class="report-modal-actions">
          <n-button size="small" type="success" @click="copyToClipboard">复制到剪切板</n-button>
          <n-button size="small" type="primary" @click="saveAsMarkdown(editorDataRef.stockCode,editorDataRef.stockName)">保存为Markdown文件</n-button>
          <n-button size="small" type="error" @click="share(editorDataRef.stockCode,editorDataRef.stockName)">分享到项目社区</n-button>
        </div>
      </div>
    </div>
  </n-modal>
</template>

<style scoped>
.report-toolbar {
  display: grid;
  grid-template-columns: minmax(320px, 1.3fr) minmax(220px, 1fr) auto;
  gap: 12px;
  align-items: center;
}

.report-toolbar-date,
.report-toolbar-keyword {
  min-width: 0;
}

.report-toolbar-button {
  min-width: 96px;
}

.report-table {
  height: calc(100vh - 210px);
  margin-top: 12px;
}

.report-modal {
  display: flex;
  align-items: center;
  justify-content: center;
}

.report-modal-shell {
  width: min(1280px, 94vw);
  max-height: 90vh;
  background: linear-gradient(180deg, #fffdfa 0%, #f8f5ef 100%);
  border-radius: 20px;
  box-shadow: 0 28px 90px rgba(20, 29, 47, 0.28);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.report-modal-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 22px 24px 18px;
  border-bottom: 1px solid rgba(32, 44, 67, 0.08);
  background: linear-gradient(135deg, rgba(250, 244, 232, 0.92) 0%, rgba(255, 255, 255, 0.96) 100%);
}

.report-modal-heading {
  min-width: 0;
  flex: 1;
}

.report-modal-title {
  font-size: 22px;
  line-height: 1.3;
  font-weight: 700;
  color: #1d2736;
}

.report-modal-meta {
  margin-top: 10px;
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
  font-size: 13px;
  color: #5f6b7b;
}

.report-modal-question {
  max-width: 100%;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.report-modal-spin {
  flex: 1;
  min-height: 0;
  display: flex;
  overflow: hidden;
}

.report-modal-body {
  flex: 1;
  display: flex;
  min-height: 0;
  padding: 0 24px;
  overflow: hidden;
}

.report-preview-wrap {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 20px 0 24px;
}

:deep(.report-modal-spin .n-spin-container),
:deep(.report-modal-spin .n-spin-content) {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.report-modal-footer {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
  padding: 16px 24px 20px;
  border-top: 1px solid rgba(32, 44, 67, 0.08);
  background: rgba(255, 255, 255, 0.96);
}

.report-modal-note {
  flex: 1;
  min-width: 260px;
  color: #9c4331;
  line-height: 1.6;
  font-size: 13px;
}

.report-modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  flex-wrap: wrap;
}

:deep(.report-preview) {
  text-align: left;
  background: transparent;
}

:deep(.report-preview .md-editor-preview-wrapper) {
  padding: 0;
  background: transparent;
}

:deep(.report-preview table) {
  display: block;
  width: 100%;
  overflow-x: auto;
}

:deep(.report-preview pre) {
  overflow-x: auto;
}

@media (max-width: 900px) {
  .report-toolbar {
    grid-template-columns: 1fr;
  }

  .report-toolbar-button {
    width: 100%;
  }

  .report-table {
    height: calc(100vh - 260px);
  }

  .report-modal-shell {
    width: 100vw;
    height: 100vh;
    max-height: 100vh;
    border-radius: 0;
  }

  .report-modal-header,
  .report-modal-body,
  .report-modal-footer {
    padding-left: 16px;
    padding-right: 16px;
  }

  .report-modal-title {
    font-size: 18px;
  }

  .report-modal-question {
    white-space: normal;
  }
}

</style>
