<script setup>
import {h, onMounted, ref} from 'vue'
import {NButton, NTag, NText, useMessage} from 'naive-ui'
import {MdPreview} from 'md-editor-v3'
import {GetAIRecommendation, ListAIRecommendations} from '../services/app-api'
import StockSparkLine from './stockSparkLine.vue'

const message = useMessage()
const loading = ref(false)
const rows = ref([])
const detailVisible = ref(false)
const detail = ref(null)

const statusLabels = {pending: '待激活', active: '已激活', sell_pending: '待卖', invalidated: '已失效', missed_cash: '错过—资金不足', closed: '已卖出'}
function dateTime(value) { return value ? String(value).slice(0, 19).replace('T', ' ') : '--' }
function statusType(status) { if (status === 'active') return 'success'; if (status === 'closed') return 'info'; if (status === 'pending' || status === 'sell_pending') return 'warning'; return 'error' }

const columns = [
  {title: '股票名称', key: 'stockName', width: 120, render: row => h(NButton, {text: true, type: 'primary', onClick: () => showDetail(row)}, {default: () => row.stockName})},
  {title: '股票代码', key: 'stockCode', width: 115},
  {title: '信号时间', key: 'signalAt', width: 170, render: row => dateTime(row.signalAt)},
  {title: 'AI 摘要', key: 'aiSummary', minWidth: 260, ellipsis: {tooltip: true}},
  {title: '激活条件', key: 'activationCondition', minWidth: 280, ellipsis: {tooltip: true}},
  {title: '主要风险', key: 'mainRisk', minWidth: 220, ellipsis: {tooltip: true}},
  {title: '当前状态', key: 'status', width: 135, render: row => h(NTag, {type: statusType(row.status), bordered: false}, {default: () => statusLabels[row.status] || row.status})},
  {title: '来源', key: 'sourceRefs', minWidth: 150, ellipsis: {tooltip: true}},
]

async function refresh() {
  loading.value = true
  try { rows.value = await ListAIRecommendations(200, 0) || [] }
  catch (error) { message.error(error?.message || String(error)) }
  finally { loading.value = false }
}

async function showDetail(row) {
  detailVisible.value = true
  detail.value = null
  try { detail.value = await GetAIRecommendation(row.recommendationId) }
  catch (error) { message.error(error?.message || String(error)) }
}

onMounted(refresh)
</script>

<template>
  <n-space vertical>
    <n-flex justify="space-between" align="center">
      <n-text depth="3">推荐只代表 AI 研究信号；点击股票名称查看独立会话和成交详情。</n-text>
      <n-button :loading="loading" @click="refresh">刷新</n-button>
    </n-flex>
    <n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="1650" :row-key="row => row.recommendationId"/>
  </n-space>

  <n-modal v-model:show="detailVisible">
    <n-card style="width:min(1220px, 95vw); max-height:92vh" title="股票推荐详情" closable @close="detailVisible = false">
      <n-scrollbar style="max-height:80vh">
        <n-spin :show="!detail">
          <template v-if="detail">
            <n-descriptions bordered :column="3" size="small">
              <n-descriptions-item label="股票">{{ detail.recommendation.stockName }}（{{ detail.recommendation.stockCode }}）</n-descriptions-item>
              <n-descriptions-item label="信号时间">{{ dateTime(detail.recommendation.signalAt) }}</n-descriptions-item>
              <n-descriptions-item label="状态">{{ statusLabels[detail.recommendation.status] || detail.recommendation.status }}</n-descriptions-item>
              <n-descriptions-item label="激活条件" :span="3">{{ detail.recommendation.activationCondition }}</n-descriptions-item>
              <n-descriptions-item label="主要风险" :span="3">{{ detail.recommendation.mainRisk }}</n-descriptions-item>
            </n-descriptions>
            <n-divider title-placement="left">分钟图</n-divider>
            <StockSparkLine :stock-code="detail.recommendation.stockCode" :stock-name="detail.recommendation.stockName" :last-price="detail.recommendation.closePrice || detail.recommendation.activationPrice" :open-price="detail.recommendation.activationPrice"/>
            <n-divider title-placement="left">完整 AI 报告</n-divider>
            <MdPreview :model-value="detail.analysis.finalReport || '暂无报告'"/>
            <n-divider title-placement="left">AI 判断时间线</n-divider>
            <n-timeline>
              <n-timeline-item v-for="item in detail.decisions" :key="item.eventId" :type="item.decisionType === '错误重试' ? 'error' : 'info'" :title="item.decisionType" :time="dateTime(item.decidedAt)">
                {{ item.reason }}
              </n-timeline-item>
            </n-timeline>
            <n-divider title-placement="left">成交与净收益</n-divider>
            <n-data-table :columns="[
              {title:'方向', key:'side'}, {title:'成交时间', key:'tradedAt', render:r=>dateTime(r.tradedAt)},
              {title:'成交价', key:'executionPrice'}, {title:'数量', key:'quantity'}, {title:'费用', key:'totalFees'}, {title:'净现金流', key:'netCashFlow'}
            ]" :data="detail.trades || []"/>
            <n-alert v-if="detail.position" type="info" style="margin-top:12px">
              数量 {{ detail.position.quantity }}，买入价 {{ detail.position.entryPrice?.toFixed?.(3) ?? detail.position.entryPrice }}，净收益 {{ detail.position.netPnl?.toFixed?.(2) ?? detail.position.netPnl }} 元
            </n-alert>
          </template>
        </n-spin>
      </n-scrollbar>
    </n-card>
  </n-modal>
</template>
