<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { GetAiRecommendStocksYieldList, GetAiRecommendYieldDailyOverview } from '../services/app-api'
import { useSharedResearchDateRange } from '../composables/useSharedResearchDateRange'
import AiRecommendYieldDailyOverviewChart from './AiRecommendYieldDailyOverviewChart.vue'

const { researchDateRangeModel, researchDateRangeKey, initSharedResearchDateRange } = useSharedResearchDateRange()

const loadingRef = ref(true)
const overviewLoadingRef = ref(true)
const rangeReadyRef = ref(false)

const totalYieldRateRef = ref(0)
const totalYieldRateTextRef = ref('--')
const benchmarkNameRef = ref('沪深300（现金流匹配）')
const benchmarkRateRef = ref(0)
const benchmarkRateTextRef = ref('--')
const excessYieldRateRef = ref(0)
const excessYieldRateTextRef = ref('--')
const strategyXirrRef = ref(0)
const strategyXirrTextRef = ref('--')
const benchmarkXirrRef = ref(0)
const benchmarkXirrTextRef = ref('--')
const excessXirrRef = ref(0)
const excessXirrTextRef = ref('--')
const maxDrawdownRef = ref(0)
const maxDrawdownTextRef = ref('--')
const winRateVsBenchmarkRef = ref(0)
const winRateVsBenchmarkTextRef = ref('--')
const medianExcessYieldRateRef = ref(0)
const medianExcessYieldRateTextRef = ref('--')
const dataAsOfRef = ref('')
const summaryRecordCountRef = ref(0)

const dailyOverviewDataRef = ref(null)
const dailyOverviewTabRef = ref('cumulative')

const startDateModel = computed({
  get() {
    const date = normalizePickerDate(researchDateRangeModel.value?.[0])
    return date ? date.getTime() : null
  },
  set(value) {
    const nextDate = normalizePickerDate(value)
    if (!nextDate) {
      return
    }
    const currentEnd = normalizePickerDate(researchDateRangeModel.value?.[1]) || nextDate
    if (nextDate.getTime() <= currentEnd.getTime()) {
      researchDateRangeModel.value = [nextDate, currentEnd]
      return
    }
    researchDateRangeModel.value = [nextDate, nextDate]
  }
})

const endDateModel = computed({
  get() {
    const date = normalizePickerDate(researchDateRangeModel.value?.[1])
    return date ? date.getTime() : null
  },
  set(value) {
    const nextDate = normalizePickerDate(value)
    if (!nextDate) {
      return
    }
    const currentStart = normalizePickerDate(researchDateRangeModel.value?.[0]) || nextDate
    if (nextDate.getTime() >= currentStart.getTime()) {
      researchDateRangeModel.value = [currentStart, nextDate]
      return
    }
    researchDateRangeModel.value = [nextDate, nextDate]
  }
})

onMounted(async () => {
  await initSharedResearchDateRange()
  rangeReadyRef.value = true
  await Promise.all([loadSummary(), loadDailyOverview()])
})

watch(researchDateRangeKey, async (nextKey, prevKey) => {
  if (!rangeReadyRef.value || !prevKey || nextKey === prevKey) {
    return
  }
  await loadSummary()
})

function normalizePickerDate(value) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return null
  }
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function formatDate(date) {
  const normalized = normalizePickerDate(date)
  if (!normalized) {
    return ''
  }
  const year = normalized.getFullYear()
  const month = String(normalized.getMonth() + 1).padStart(2, '0')
  const day = String(normalized.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function currentRangeParams() {
  const range = researchDateRangeModel.value || []
  return {
    startDate: formatDate(range[0]),
    endDate: formatDate(range[1])
  }
}

async function loadSummary() {
  loadingRef.value = true
  try {
    const { startDate, endDate } = currentRangeParams()
    const result = await GetAiRecommendStocksYieldList({
      page: 1,
      pageSize: 1,
      modelName: '',
      stockName: '',
      stockCode: '',
      bkName: '',
      startDate,
      endDate,
      yieldMode: 'strict'
    })
    totalYieldRateRef.value = Number(result?.totalYieldRate || 0)
    totalYieldRateTextRef.value = result?.totalYieldRateText || '--'
    benchmarkNameRef.value = result?.benchmarkName || '沪深300（现金流匹配）'
    benchmarkRateRef.value = Number(result?.benchmarkRate || 0)
    benchmarkRateTextRef.value = result?.benchmarkRateText || '--'
    excessYieldRateRef.value = Number(result?.excessYieldRate || 0)
    excessYieldRateTextRef.value = result?.excessYieldRateText || '--'
    strategyXirrRef.value = Number(result?.strategyXirr || 0)
    strategyXirrTextRef.value = result?.strategyXirrText || '--'
    benchmarkXirrRef.value = Number(result?.benchmarkXirr || 0)
    benchmarkXirrTextRef.value = result?.benchmarkXirrText || '--'
    excessXirrRef.value = Number(result?.excessXirr || 0)
    excessXirrTextRef.value = result?.excessXirrText || '--'
    maxDrawdownRef.value = Number(result?.maxDrawdown || 0)
    maxDrawdownTextRef.value = result?.maxDrawdownText || '--'
    winRateVsBenchmarkRef.value = Number(result?.winRateVsBenchmark || 0)
    winRateVsBenchmarkTextRef.value = result?.winRateVsBenchmarkText || '--'
    medianExcessYieldRateRef.value = Number(result?.medianExcessYieldRate || 0)
    medianExcessYieldRateTextRef.value = result?.medianExcessYieldRateText || '--'
    dataAsOfRef.value = result?.dataAsOf || ''
    summaryRecordCountRef.value = Number(result?.total || 0)
  } catch (error) {
    console.error('loadSummary failed', error)
  } finally {
    loadingRef.value = false
  }
}

async function loadDailyOverview() {
  overviewLoadingRef.value = true
  try {
    const result = await GetAiRecommendYieldDailyOverview()
    dailyOverviewDataRef.value = result || {
      calcMode: 'strict',
      warnings: ['读取全库收益走势失败，请稍后重试'],
      points: []
    }
  } catch (error) {
    console.error('loadDailyOverview failed', error)
    dailyOverviewDataRef.value = {
      calcMode: 'strict',
      warnings: ['读取全库收益走势失败，请稍后重试'],
      points: []
    }
  } finally {
    overviewLoadingRef.value = false
  }
}

function totalYieldTextType() {
  if (totalYieldRateTextRef.value === '--') {
    return 'default'
  }
  if (totalYieldRateRef.value > 0) {
    return 'error'
  }
  if (totalYieldRateRef.value < 0) {
    return 'success'
  }
  return 'default'
}

function benchmarkTextType() {
  if (benchmarkRateTextRef.value === '--') {
    return 'default'
  }
  if (benchmarkRateRef.value > 0) {
    return 'error'
  }
  if (benchmarkRateRef.value < 0) {
    return 'success'
  }
  return 'default'
}

function excessYieldTextType() {
  if (excessYieldRateTextRef.value === '--') {
    return 'default'
  }
  if (excessYieldRateRef.value > 0) {
    return 'error'
  }
  if (excessYieldRateRef.value < 0) {
    return 'success'
  }
  return 'default'
}

function metricTextType(value, text) {
  if (text === '--') {
    return 'default'
  }
  if (Number(value) > 0) {
    return 'error'
  }
  if (Number(value) < 0) {
    return 'success'
  }
  return 'default'
}

function drawdownTextType() {
  if (maxDrawdownTextRef.value === '--') {
    return 'default'
  }
  if (maxDrawdownRef.value < 0) {
    return 'success'
  }
  return 'default'
}

function dailyOverviewSummaryText() {
  const data = dailyOverviewDataRef.value
  if (!data) {
    return '--'
  }
  const total = Number(data.totalRecordCount || 0)
  const included = Number(data.includedRecordCount || 0)
  const skipped = Number(data.skippedRecordCount || 0)
  return `总记录 ${total}，纳入 ${included}，跳过 ${skipped}`
}

function dailyOverviewRangeText() {
  const data = dailyOverviewDataRef.value
  if (!data) {
    return '--'
  }
  const start = String(data.rangeStart || '').trim()
  const end = String(data.rangeEnd || '').trim()
  if (!start && !end) {
    return '--'
  }
  return `${start || '--'} -> ${end || '--'}`
}

function dailyOverviewWarningText() {
  const warnings = Array.isArray(dailyOverviewDataRef.value?.warnings) ? dailyOverviewDataRef.value.warnings : []
  if (warnings.length === 0) {
    return ''
  }
  return warnings.join('；')
}

function benchmarkTooltipText() {
  return `${benchmarkNameRef.value} 会按策略真实买入/卖出现金流去匹配同口径的基准收益，避免把一次性满仓指数涨跌拿来和分批买入策略硬比。`
}
</script>

<template>
  <div class="yield-stats-page">
    <n-space vertical size="large">
      <n-card size="small" title="统计范围">
        <n-input-group>
          <n-date-picker v-model:value="startDateModel" type="date" style="width: 22%" />
          <n-input value="至" readonly style="width: 8%; text-align: center;" />
          <n-date-picker v-model:value="endDateModel" type="date" style="width: 22%" />
          <n-button type="primary" ghost :loading="loadingRef" @click="loadSummary">
            刷新统计
          </n-button>
          <n-button type="info" ghost :loading="overviewLoadingRef" @click="loadDailyOverview">
            刷新全库走势
          </n-button>
        </n-input-group>
        <div class="yield-stats-toolbar-hint">
          <n-text depth="3">当前页专注收益率统计与可视化；个股明细、分钟回放和手动补算仍保留在“股票收益率”栏目。</n-text>
        </div>
      </n-card>

      <n-grid x-gap="12" y-gap="12" cols="1 s:2 m:3 l:4" responsive="screen">
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">策略收益率</div>
              </template>
              当前纳入统计的可执行推荐，按统一交易成本口径汇总后的总净收益率。它回答的是整套策略最终赚了多少。
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="totalYieldTextType()">{{ totalYieldRateTextRef }}</n-text>
            </div>
            <div class="metric-meta">统计记录：{{ summaryRecordCountRef }} 条</div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">{{ benchmarkNameRef }}</div>
              </template>
              {{ benchmarkTooltipText() }}
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="benchmarkTextType()">{{ benchmarkRateTextRef }}</n-text>
            </div>
            <div class="metric-meta">现金流匹配基准</div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">超额收益率</div>
              </template>
              策略收益率减去基准收益率后的差值。大于 0 代表整体跑赢基准，小于 0 代表没有跑赢。
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="excessYieldTextType()">{{ excessYieldRateTextRef }}</n-text>
            </div>
            <div class="metric-meta">策略减基准</div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">最大回撤</div>
              </template>
              从任一阶段高点往后看，净值曾经最大跌回去多少。它衡量的是“最难熬的时候有多痛”，越接近 0 通常越稳。
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="drawdownTextType()">{{ maxDrawdownTextRef }}</n-text>
            </div>
            <div class="metric-meta">越接近 0 越稳</div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">策略 XIRR</div>
              </template>
              把每次真实买入、卖出发生的时间点都算进去后，折算出来的年化收益率。适合衡量分批进出、现金流不规则的策略表现。
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="metricTextType(strategyXirrRef, strategyXirrTextRef)">{{ strategyXirrTextRef }}</n-text>
            </div>
            <div class="metric-meta">考虑现金流时点</div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">基准 XIRR</div>
              </template>
              用和策略同样的现金流时点去买入并持有基准指数，再折算出的年化收益率。它回答的是“如果同样的钱拿去买基准会怎样”。
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="metricTextType(benchmarkXirrRef, benchmarkXirrTextRef)">{{ benchmarkXirrTextRef }}</n-text>
            </div>
            <div class="metric-meta">{{ benchmarkNameRef }}</div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">超额 XIRR</div>
              </template>
              策略 XIRR 减去基准 XIRR。它比普通超额收益率更适合看分批买入、持仓时长不一致时，策略是否真的在资金效率上跑赢基准。
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="metricTextType(excessXirrRef, excessXirrTextRef)">{{ excessXirrTextRef }}</n-text>
            </div>
            <div class="metric-meta">更适合分批买入策略</div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" class="metric-card">
            <n-tooltip trigger="hover">
              <template #trigger>
                <div class="metric-label metric-label-help">跑赢基准占比</div>
              </template>
              纳入统计的记录里，有多少比例的单笔推荐最终收益高于对应基准收益。它看的是稳定性，而不只是少数大赚样本。
            </n-tooltip>
            <div class="metric-value">
              <n-text :type="metricTextType(winRateVsBenchmarkRef, winRateVsBenchmarkTextRef)">{{ winRateVsBenchmarkTextRef }}</n-text>
            </div>
            <div class="metric-meta">看稳定性，不只看爆发力</div>
          </n-card>
        </n-grid-item>
      </n-grid>

      <n-grid x-gap="12" y-gap="12" cols="1 s:1 m:2" responsive="screen">
        <n-grid-item>
          <n-card size="small" title="超额表现概览">
            <div class="detail-row">
              <n-tooltip trigger="hover">
                <template #trigger>
                  <span class="detail-label detail-label-help">超额收益中位数</span>
                </template>
                把每条记录的超额收益率从小到大排序后，取中间那个值。它比平均值更不容易被极端大赚或大亏样本带偏。
              </n-tooltip>
              <n-text :type="metricTextType(medianExcessYieldRateRef, medianExcessYieldRateTextRef)">
                {{ medianExcessYieldRateTextRef }}
              </n-text>
            </div>
            <div class="detail-row">
              <n-tooltip trigger="hover">
                <template #trigger>
                  <span class="detail-label detail-label-help">数据时间</span>
                </template>
                当前统计结果最近一次完成刷新或回算的时间。若它较旧，说明页面里的收益率不一定是最新状态。
              </n-tooltip>
              <n-text depth="3">{{ dataAsOfRef || '--' }}</n-text>
            </div>
            <div class="detail-row">
              <span class="detail-label">观察建议</span>
              <n-text depth="3">优先一起看超额收益、超额 XIRR、回撤和跑赢占比，不要只盯总收益率。</n-text>
            </div>
          </n-card>
        </n-grid-item>
        <n-grid-item>
          <n-card size="small" title="口径说明">
            <div class="detail-row">
              <span class="detail-label">比较方式</span>
              <n-text depth="3">基准使用“{{ benchmarkNameRef }}”现金流匹配口径，更适合和分批买入的策略做同口径比较。</n-text>
            </div>
            <div class="detail-row">
              <span class="detail-label">适合判断什么</span>
              <n-text depth="3">是否真正跑赢基准、收益是否来自择时与选股，而不是单纯被大盘抬起来。</n-text>
            </div>
            <div class="detail-row">
              <span class="detail-label">不适合判断什么</span>
              <n-text depth="3">单看某一天涨跌或某一只股票的盈利，无法直接代表整套策略是否优秀。</n-text>
            </div>
          </n-card>
        </n-grid-item>
      </n-grid>

      <n-card size="small" title="全库收益走势">
        <template #header-extra>
          <n-text depth="3">{{ dailyOverviewSummaryText() }}</n-text>
        </template>
        <div class="chart-meta-row">
          <n-text depth="3">范围：{{ dailyOverviewRangeText() }}</n-text>
          <n-text depth="3">数据时间：{{ dailyOverviewDataRef?.dataAsOf || '--' }}</n-text>
          <n-text depth="3">口径：{{ dailyOverviewDataRef?.calcMode || 'strict' }}</n-text>
        </div>
        <n-alert
          v-if="dailyOverviewWarningText()"
          type="warning"
          :show-icon="false"
          style="margin-bottom: 12px; text-align: left;"
        >
          {{ dailyOverviewWarningText() }}
        </n-alert>
        <n-spin :show="overviewLoadingRef">
          <div v-if="dailyOverviewDataRef">
            <n-tabs v-model:value="dailyOverviewTabRef" type="line" animated>
              <n-tab-pane name="cumulative" tab="累计走势">
                <ai-recommend-yield-daily-overview-chart
                  :overview-data="dailyOverviewDataRef"
                  mode="cumulative"
                />
              </n-tab-pane>
              <n-tab-pane name="daily" tab="单日变化">
                <ai-recommend-yield-daily-overview-chart
                  :overview-data="dailyOverviewDataRef"
                  mode="daily"
                />
              </n-tab-pane>
            </n-tabs>
          </div>
          <n-empty v-else description="暂无全库收益走势数据" />
        </n-spin>
      </n-card>
    </n-space>
  </div>
</template>

<style scoped>
.yield-stats-page {
  min-height: calc(100vh - 170px);
}

.yield-stats-toolbar-hint {
  margin-top: 10px;
}

.metric-card {
  height: 100%;
  border-radius: 14px;
}

.metric-label {
  color: #64748b;
  font-size: 13px;
  line-height: 1.5;
}

.metric-label-help,
.detail-label-help {
  cursor: help;
  text-decoration: underline dotted rgba(100, 116, 139, 0.6);
  text-underline-offset: 3px;
}

.metric-value {
  margin-top: 10px;
  font-size: 28px;
  font-weight: 700;
  line-height: 1.2;
}

.metric-meta {
  margin-top: 10px;
  color: #94a3b8;
  font-size: 12px;
}

.detail-row + .detail-row {
  margin-top: 10px;
}

.detail-label {
  display: inline-block;
  min-width: 112px;
  color: #475569;
  font-weight: 600;
}

.chart-meta-row {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
</style>
