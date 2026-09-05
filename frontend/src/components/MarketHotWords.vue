<script setup>
import {NTag, NText} from 'naive-ui'
import {computed, h, ref, toRef, watch} from 'vue'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import {GetMarketHotWords, GlobalStockIndexes} from '../services/market-api.js'
import EvidenceStatusBar from './EvidenceStatusBar.vue'
import {
  confidencePresentation,
  formatDocumentShare,
  hotWordTrend,
  normalizeHotWordsPayload,
  sentimentScale,
} from './market-hot-words-model.js'

const props = defineProps({
  active: {type: Boolean, default: false},
  darkTheme: {type: Boolean, default: false},
})

const active = toRef(props, 'active')
const hours = 24
const baselineDays = 7
const limit = 30
const expandedRowKeys = ref([])

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
const sentiment = computed(() => sentimentScale(payload.value.sentiment.score))
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

watch(rows, nextRows => {
  const keys = new Set(nextRows.map(row => row._key))
  expandedRowKeys.value = expandedRowKeys.value.filter(key => keys.has(key))
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
            <div class="sentiment-scale">
              <div class="sentiment-reading" :class="`sentiment-${sentiment.tone}`">
                <strong>{{ sentiment.score.toFixed(1) }}</strong>
                <span>{{ payload.sentiment.label || '市场情绪' }}</span>
              </div>
              <div class="sentiment-pointer-lane" aria-hidden="true">
                <span class="sentiment-pointer" :style="{left: `${sentiment.position}%`}"/>
              </div>
              <div class="sentiment-band" aria-hidden="true"/>
              <div class="sentiment-labels">
                <span>冰点</span><span>谨慎</span><span>中性</span><span>乐观</span><span>极热</span>
              </div>
            </div>
            <n-flex justify="center" :wrap="true" :size="8">
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

.sentiment-scale {
  padding: 22px 8px 30px;
}

.sentiment-reading {
  display: flex;
  align-items: baseline;
  justify-content: center;
  gap: 8px;
  min-height: 42px;
}

.sentiment-reading strong {
  font-size: 28px;
  line-height: 1;
}

.sentiment-reading span {
  font-size: 14px;
}

.sentiment-ice { color: #18a058; }
.sentiment-cautious { color: #36adcf; }
.sentiment-optimistic { color: #f0a020; }
.sentiment-hot { color: #d03050; }

.sentiment-pointer-lane {
  position: relative;
  height: 25px;
  margin-top: 8px;
}

.sentiment-pointer {
  position: absolute;
  bottom: 0;
  width: 2px;
  height: 19px;
  color: var(--n-text-color, #334155);
  background: currentColor;
  transform: translateX(-50%);
}

.sentiment-pointer::after {
  position: absolute;
  bottom: -1px;
  left: 50%;
  width: 0;
  height: 0;
  border: 6px solid transparent;
  border-top: 8px solid currentColor;
  content: '';
  transform: translate(-50%, 85%);
}

.sentiment-band {
  height: 14px;
  border: 1px solid rgba(128, 128, 128, 0.24);
  border-radius: 5px;
  background: linear-gradient(to right, #18a058 0 25%, #36adcf 25% 50%, #f0a020 50% 75%, #d03050 75% 100%);
}

.sentiment-labels {
  position: relative;
  height: 18px;
  margin-top: 9px;
  color: var(--n-text-color-3, #6b7280);
  font-size: 12px;
}

.sentiment-labels span { position: absolute; white-space: nowrap; }
.sentiment-labels span:first-child { left: 0; }
.sentiment-labels span:nth-child(2) { left: 25%; transform: translateX(-50%); }
.sentiment-labels span:nth-child(3) { left: 50%; transform: translateX(-50%); }
.sentiment-labels span:nth-child(4) { left: 75%; transform: translateX(-50%); }
.sentiment-labels span:last-child { right: 0; }

@media (max-width: 700px) {
  .sentiment-scale { padding-inline: 2px; }
  .sentiment-labels { font-size: 11px; }
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
