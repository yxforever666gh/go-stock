<script setup>
import * as echarts from 'echarts'
import {NTag, NText} from 'naive-ui'
import {computed, h, nextTick, onBeforeUnmount, onMounted, ref, toRef, watch} from 'vue'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import {GetMarketHotWords, GlobalStockIndexes} from '../services/market-api.js'
import EvidenceStatusBar from './EvidenceStatusBar.vue'
import {
  confidencePresentation,
  formatDocumentShare,
  hotWordTrend,
  normalizeHotWordsPayload,
} from './market-hot-words-model.js'

const props = defineProps({
  active: {type: Boolean, default: false},
  darkTheme: {type: Boolean, default: false},
})

const active = toRef(props, 'active')
const hours = 24
const baselineDays = 7
const limit = 30
const gaugeElement = ref(null)
const expandedRowKeys = ref([])
let gaugeChart = null
let resizeObserver = null

const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {window: {}, baseline: {available: false}, currentDocumentCount: 0, sentiment: {score: 0, label: '中性'}, items: []},
  intervalMs: 300000,
  loader: () => GetMarketHotWords({hours, baselineDays, limit}),
  requestKey: `market-hot-words|${hours}|${baselineDays}|${limit}`,
  session: 'always',
})

const {data: indexesData} = useMarketDataResource({
  active,
  fallbackData: {},
  intervalMs: 300000,
  loader: GlobalStockIndexes,
  requestKey: 'market-hot-words|global-indexes',
  session: 'always',
})

const payload = computed(() => normalizeHotWordsPayload(data.value))
const rows = computed(() => payload.value.items)
const sentimentScore = computed(() => Math.min(100, Math.max(-100, payload.value.sentiment.score)))
const baselineAvailable = computed(() => payload.value.baseline.available)
const mainIndex = computed(() => {
  const source = indexesData.value || {}
  const locations = new Set(['上海', '深圳', '香港', '台湾', '北京', '东京', '首尔', '纽约', '纳斯达克'])
  return [...(Array.isArray(source.asia) ? source.asia : []), ...(Array.isArray(source.america) ? source.america : [])]
    .filter(item => locations.has(item?.location))
})

const statusNotices = computed(() => {
  const notices = [...new Set((envelope.value?.warnings || []).map(String).filter(Boolean))]
  if (envelope.value?.status === 'partial' && !notices.length) notices.push('当前结果为降级数据，排名可能按新闻覆盖量计算。')
  if (envelope.value?.status === 'stale') notices.push('刷新失败，当前展示上一次成功获取的热词数据。')
  return [...new Set(notices)]
})

function dateTime(value) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return String(value).replace('T', ' ').slice(0, 19)
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(date).replaceAll('/', '-')
}

function renderRepresentativeNews(row) {
  return h('div', {class: 'representative-news-list'}, row.representativeNews.map((news, index) => h('article', {
    key: news._key,
    class: 'representative-news-item',
  }, [
    h('div', {class: 'representative-news-meta'}, [
      h('span', dateTime(news.publishedAt)),
      h('span', news.source),
      news.url ? h('a', {href: news.url, target: '_blank', rel: 'noopener noreferrer'}, `查看原文 ${index + 1}`) : null,
    ]),
    h('div', {class: 'representative-news-excerpt'}, news.excerpt || '暂无摘要'),
  ])))
}

const columns = computed(() => [
  {
    type: 'expand',
    width: 42,
    expandable: row => row.representativeNews.length > 0,
    renderExpand: renderRepresentativeNews,
  },
  {title: '排名', key: 'rank', width: 68, render: row => `#${row.rank}`},
  {
    title: '热词', key: 'word', minWidth: 130, ellipsis: {tooltip: true},
    render: row => h(NText, {strong: true}, {default: () => row.word || '--'}),
  },
  {title: '提及篇数', key: 'documentCount', width: 96, sorter: (left, right) => left.documentCount - right.documentCount},
  {title: '24h 占比', key: 'documentShare', width: 102, render: row => formatDocumentShare(row.documentShare)},
  {
    title: '较基线变化', key: 'growthPct', width: 142,
    render: row => {
      const trend = hotWordTrend(row, baselineAvailable.value)
      return h(NTag, {size: 'small', bordered: false, type: trend.type}, {default: () => trend.label})
    },
  },
  {
    title: '来源数', key: 'sourceCount', width: 82,
    render: row => h(NText, {title: row.sources.join('、') || '未标注来源'}, {default: () => String(row.sourceCount)}),
  },
  {
    title: '置信度', key: 'confidence', width: 82,
    render: row => {
      const confidence = confidencePresentation(row.confidence)
      return h(NTag, {size: 'small', bordered: false, type: confidence.type}, {default: () => confidence.label})
    },
  },
])

function rowProps(row) {
  return {
    class: row.representativeNews.length ? 'hot-word-row' : '',
    onClick: event => {
      if (!row.representativeNews.length || event.target?.closest?.('.n-data-table-expand-trigger')) return
      const next = new Set(expandedRowKeys.value)
      if (next.has(row._key)) next.delete(row._key)
      else next.add(row._key)
      expandedRowKeys.value = [...next]
    },
  }
}

function renderGauge() {
  if (!gaugeChart || gaugeChart.isDisposed()) return
  const textColor = props.darkTheme ? '#d6d6d6' : '#465568'
  gaugeChart.setOption({
    animation: true,
    backgroundColor: 'transparent',
    series: [{
      type: 'gauge',
      startAngle: 180,
      endAngle: 0,
      center: ['50%', '72%'],
      radius: '92%',
      min: -100,
      max: 100,
      splitNumber: 4,
      axisLine: {lineStyle: {width: 8, color: [[0.25, '#18a058'], [0.5, '#36adcf'], [0.75, '#f0a020'], [1, '#d03050']]}},
      pointer: {length: '56%', width: 7, itemStyle: {color: 'auto'}},
      axisTick: {distance: -13, length: 6, lineStyle: {color: 'auto', width: 1}},
      splitLine: {distance: -17, length: 14, lineStyle: {color: 'auto', width: 2}},
      axisLabel: {
        color: textColor,
        distance: -41,
        fontSize: 12,
        formatter: value => ({'-100': '冰点', '-50': '谨慎', '0': '中性', '50': '乐观', '100': '极热'}[String(value)] || ''),
      },
      title: {offsetCenter: [0, '-5%'], color: textColor, fontSize: 15},
      detail: {
        valueAnimation: true,
        offsetCenter: [0, '-30%'],
        color: 'inherit',
        fontSize: 28,
        formatter: value => value.toFixed(1),
      },
      data: [{value: sentimentScore.value, name: payload.value.sentiment.label || '市场情绪'}],
    }],
  }, true)
}

watch([sentimentScore, () => payload.value.sentiment.label, () => props.darkTheme], async () => {
  await nextTick()
  renderGauge()
})

watch(rows, nextRows => {
  const keys = new Set(nextRows.map(row => row._key))
  expandedRowKeys.value = expandedRowKeys.value.filter(key => keys.has(key))
})

onMounted(() => {
  if (!gaugeElement.value) return
  gaugeChart = echarts.init(gaugeElement.value)
  resizeObserver = new ResizeObserver(() => gaugeChart?.resize())
  resizeObserver.observe(gaugeElement.value)
  renderGauge()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  if (gaugeChart && !gaugeChart.isDisposed()) gaugeChart.dispose()
  gaugeChart = null
})
</script>

<template>
  <n-collapse :default-expanded-names="['hot-words']" :trigger-areas="['main', 'extra', 'arrow']" display-directive="show">
    <n-collapse-item name="hot-words">
      <template #header>
        <n-flex :wrap="true" :size="6">
          <n-tag
            v-for="(item, index) in mainIndex"
            :key="`${item.location}-${item.name}-${index}`"
            size="small"
            :bordered="false"
            :type="Number(item.zdf) > 0 ? 'error' : (Number(item.zdf) < 0 ? 'success' : 'default')"
          >
            <n-flex align="center" :wrap="false" :size="5">
              <n-image v-if="item.img" :width="18" :src="item.img" preview-disabled/>
              <n-text :type="Number(item.zdf) > 0 ? 'error' : (Number(item.zdf) < 0 ? 'success' : 'default')">
                {{ item.name }} {{ item.zxj }}
              </n-text>
              <span>{{ Number.isFinite(Number(item.zdf)) ? `${Number(item.zdf) > 0 ? '+' : ''}${Number(item.zdf).toFixed(2)}%` : '--' }}</span>
            </n-flex>
          </n-tag>
          <n-text v-if="!mainIndex.length" depth="3">主要股指加载中</n-text>
        </n-flex>
      </template>
      <template #header-extra>最近24小时突增热词</template>

      <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
      <n-alert v-for="notice in statusNotices" :key="notice" type="warning" :bordered="false" class="hot-words-notice">
        {{ notice }}
      </n-alert>

      <n-grid cols="1 l:24" responsive="screen" :x-gap="14" :y-gap="12">
        <n-gi span="1 l:6">
          <n-card size="small" title="市场情绪强弱" class="sentiment-card">
            <div ref="gaugeElement" class="sentiment-gauge"/>
            <n-flex justify="center" :wrap="true" :size="8">
              <n-tag :bordered="false" type="info">{{ payload.sentiment.label }}</n-tag>
              <n-text depth="3">样本 {{ payload.currentDocumentCount }} 篇</n-text>
            </n-flex>
          </n-card>
        </n-gi>
        <n-gi span="1 l:18">
          <n-card size="small" class="hot-words-card">
            <template #header>
              <n-flex align="center" :wrap="true" :size="8">
                <n-text strong>热词排行榜</n-text>
                <n-tag size="small" :bordered="false" :type="baselineAvailable ? 'success' : 'warning'">
                  {{ baselineAvailable ? '7日突增基线' : '覆盖量降级模式' }}
                </n-tag>
              </n-flex>
            </template>
            <n-spin :show="loading && !rows.length" description="正在计算最近24小时热词">
              <n-data-table
                v-if="rows.length"
                v-model:expanded-row-keys="expandedRowKeys"
                :columns="columns"
                :data="rows"
                :row-key="row => row._key"
                :row-props="rowProps"
                :scroll-x="850"
                :max-height="460"
                striped
              />
              <n-result
                v-else-if="!loading && envelope.status === 'unavailable'"
                status="error"
                title="热词数据暂不可用"
                :description="error || '请稍后刷新重试'"
              >
                <template #footer><n-button secondary @click="refresh">重新加载</n-button></template>
              </n-result>
              <n-empty v-else-if="!loading" description="最近24小时暂无满足门槛的热词" class="hot-words-empty"/>
            </n-spin>
          </n-card>
        </n-gi>
      </n-grid>
    </n-collapse-item>
  </n-collapse>
</template>

<style scoped>
.hot-words-notice {
  margin-bottom: 8px;
}

.sentiment-card,
.hot-words-card {
  height: 100%;
}

.sentiment-gauge {
  width: 100%;
  height: 300px;
}

.hot-words-empty {
  min-height: 300px;
  justify-content: center;
}

:deep(.hot-word-row) {
  cursor: pointer;
}

:deep(.representative-news-list) {
  display: grid;
  gap: 9px;
  padding: 5px 12px 10px;
}

:deep(.representative-news-item) {
  padding: 10px 12px;
  border: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.24));
  border-radius: 6px;
  background: var(--n-merged-td-color, rgba(128, 128, 128, 0.05));
}

:deep(.representative-news-meta) {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 14px;
  margin-bottom: 5px;
  color: var(--n-td-text-color, #6b7280);
  font-size: 12px;
}

:deep(.representative-news-meta a) {
  color: var(--n-primary-color, #18a058);
  text-decoration: none;
}

:deep(.representative-news-excerpt) {
  line-height: 1.6;
  white-space: normal;
}
</style>
