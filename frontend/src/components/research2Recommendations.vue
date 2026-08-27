<script setup>
import {h, onMounted, ref} from 'vue'
import {NButton, NTag, useMessage} from 'naive-ui'
import {GetResearch2Recommendation, ListResearch2Recommendations} from '../services/research2-api'
import AppMarkdownPreview from './AppMarkdownPreview.vue'
import {formatInteger, formatMoney, formatNumber, formatPercent, formatPrice} from '../utils/number-format'

const message = useMessage(), loading = ref(false), rows = ref([]), detail = ref(null), visible = ref(false)
const dateTime = value => value ? String(value).slice(0, 19).replace('T', ' ') : '--'
const statusLabels = {buy_pending: '待买入', active: '持仓中', sell_pending: '待卖出', closed: '已平仓', missed_cash: '资金不足', missed_untradable: '不可成交', missed_window: '错过窗口', cancelled_price: '价格取消'}
const statusType = status => status === 'closed' ? 'success' : ['missed_cash','missed_untradable','missed_window','cancelled_price'].includes(status) ? 'error' : 'warning'
async function show(row) { visible.value = true; detail.value = null; try { detail.value = await GetResearch2Recommendation(row.recommendationId) } catch (error) { message.error(error?.message || String(error)) } }
const columns = [
  {title: '信号时间', key: 'signalAt', width: 170, render: row => dateTime(row.signalAt)},
  {title: '排名', key: 'rank', width: 70},
  {title: '股票', key: 'stockCode', minWidth: 160, render: row => `${row.stockName}（${row.stockCode}）`},
  {title: '最终分', key: 'finalScore', width: 90, render: row => formatNumber(row.finalScore, 1)},
  {title: '参考价', key: 'referencePrice', width: 95, render: row => formatPrice(row.referencePrice)},
  {title: '买入区间', key: 'buyLower', width: 150, render: row => `${formatPrice(row.buyLower)}–${formatPrice(row.buyUpper)}`},
  {title: '目标买入', key: 'targetBuyAt', width: 170, render: row => `${dateTime(row.targetBuyAt)}${row.late ? '（迟到）' : ''}`},
  {title: '成交数量', key: 'quantity', width: 100, render: row => formatInteger(row.quantity)},
  {title: '买/卖价', key: 'buyPrice', width: 150, render: row => `${formatPrice(row.buyPrice)} / ${formatPrice(row.sellPrice)}`},
  {title: '净收益', key: 'netPnl', width: 110, render: row => formatMoney(row.netPnl)},
  {title: '收益率', key: 'netYieldRate', width: 100, render: row => formatPercent(row.netYieldRate)},
  {title: '状态', key: 'status', width: 110, render: row => h(NTag, {type: statusType(row.status), bordered: false}, {default: () => statusLabels[row.status] || row.status})},
  {title: '操作', key: 'action', width: 90, render: row => h(NButton, {size: 'small', tertiary: true, type: 'primary', onClick: () => show(row)}, {default: () => '详情'})},
]
async function refresh() { loading.value = true; try { rows.value = await ListResearch2Recommendations() } catch (error) { message.error(error?.message || String(error)) } finally { loading.value = false } }
onMounted(refresh)
</script>

<template>
  <n-space vertical><n-flex justify="space-between"><n-text depth="3">实际可买标的按数量等额分配可用现金，向下取整为100股整手并计入交易费用。</n-text><n-button :loading="loading" @click="refresh">刷新</n-button></n-flex><n-data-table :columns="columns" :data="rows" :loading="loading" :scroll-x="1660" :row-key="row => row.recommendationId"/></n-space>
  <n-modal v-model:show="visible"><n-card title="推荐与成交详情" closable style="width:min(1280px,94vw);max-height:94vh" @close="visible=false"><n-scrollbar style="max-height:82vh"><n-spin :show="!detail"><template v-if="detail"><n-descriptions bordered :column="3"><n-descriptions-item label="股票">{{detail.recommendation.stockName}}（{{detail.recommendation.stockCode}}）</n-descriptions-item><n-descriptions-item label="评分">{{formatNumber(detail.recommendation.finalScore,1)}}</n-descriptions-item><n-descriptions-item label="状态">{{statusLabels[detail.recommendation.status] || detail.recommendation.status}}</n-descriptions-item><n-descriptions-item label="入选理由" :span="3">{{detail.recommendation.summary}}</n-descriptions-item><n-descriptions-item label="关键量化" :span="3">{{detail.recommendation.quantData}}</n-descriptions-item><n-descriptions-item label="新催化" :span="3">{{detail.recommendation.freshCatalyst || '无可核验新催化'}}</n-descriptions-item><n-descriptions-item label="主要风险" :span="3">{{detail.recommendation.mainRisk}}</n-descriptions-item><n-descriptions-item label="取消条件" :span="3">{{detail.recommendation.cancelConditions}}</n-descriptions-item></n-descriptions><n-divider>完整报告</n-divider><AppMarkdownPreview :model-value="detail.analysis.reportMarkdown || '暂无报告'"/><n-divider>成交记录</n-divider><n-data-table :data="detail.trades || []" :columns="[{title:'方向',key:'side'},{title:'时间',key:'tradedAt',render:r=>dateTime(r.tradedAt)},{title:'市场价',key:'marketPrice',render:r=>formatPrice(r.marketPrice)},{title:'成交价',key:'executionPrice',render:r=>formatPrice(r.executionPrice)},{title:'数量',key:'quantity',render:r=>formatInteger(r.quantity)},{title:'净现金流',key:'netCashFlow',render:r=>formatMoney(r.netCashFlow)}]"/></template></n-spin></n-scrollbar></n-card></n-modal>
</template>
