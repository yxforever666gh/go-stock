<script setup>
import {computed} from 'vue'
import EvidenceStatusBar from '../EvidenceStatusBar.vue'
import {useMarketDataResource} from '../../composables/useMarketDataResource.js'
import {GetTheme, ListThemeCatalysts, ListThemeDailySnapshots} from '../../services/themes-api.js'
import {
  credibilityPercent,
  normalizeThemeDetail,
  stageType,
  stanceLabel,
  themeCatalysts,
  themeSnapshots,
} from './theme-model.js'
import ThemeLifecycleTimeline from './ThemeLifecycleTimeline.vue'

const props = defineProps({
  show: {type: Boolean, default: false},
  themeId: {type: String, default: ''},
  date: {type: String, default: ''},
})
const emit = defineEmits(['update:show'])

const visible = computed({get: () => props.show, set: value => emit('update:show', value)})
const active = computed(() => props.show && Boolean(props.themeId))
const detailKey = computed(() => ['theme-detail', props.themeId, props.date].join('|'))
const historyKey = computed(() => ['theme-history', props.themeId, historyFrom.value, props.date].join('|'))
const catalystsKey = computed(() => ['theme-catalysts', props.themeId, props.date].join('|'))
const historyFrom = computed(() => subtractDays(props.date, 120))

const detailResource = useMarketDataResource({
  active,
  fallbackData: {theme: null, snapshot: null, constituents: [], catalystSummary: {}},
  intervalMs: 300000,
  loader: () => GetTheme(props.themeId, {date: props.date}),
  requestKey: detailKey,
  session: 'always',
})
const historyResource = useMarketDataResource({
  active,
  fallbackData: {themeId: '', items: []},
  intervalMs: 300000,
  loader: () => ListThemeDailySnapshots(props.themeId, {from: historyFrom.value, to: props.date, limit: 100}),
  requestKey: historyKey,
  session: 'always',
})
const catalystResource = useMarketDataResource({
  active,
  fallbackData: {themeId: '', tradeDate: '', items: []},
  intervalMs: 300000,
  loader: () => ListThemeCatalysts(props.themeId, {date: props.date, limit: 100}),
  requestKey: catalystsKey,
  session: 'always',
})

const detail = computed(() => normalizeThemeDetail(detailResource.data.value))
const snapshots = computed(() => themeSnapshots(historyResource.data.value))
const catalysts = computed(() => themeCatalysts(catalystResource.data.value))
const loading = computed(() => detailResource.loading.value || historyResource.loading.value || catalystResource.loading.value)

function subtractDays(value, count) {
  const parsed = new Date(`${value || new Date().toISOString().slice(0, 10)}T00:00:00Z`)
  if (Number.isNaN(parsed.getTime())) return ''
  parsed.setUTCDate(parsed.getUTCDate() - count)
  return parsed.toISOString().slice(0, 10)
}

function dateTime(value) {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return String(value).replace('T', ' ').slice(0, 19)
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
    timeZone: 'Asia/Shanghai',
  }).format(date).replaceAll('/', '-')
}

function safeSourceRef(value) {
  try {
    const parsed = new URL(String(value || ''))
    return ['http:', 'https:'].includes(parsed.protocol) ? parsed.toString() : ''
  } catch (_) {
    return ''
  }
}

function closeDrawer() {
  visible.value = false
}
</script>

<template>
  <n-drawer v-model:show="visible" :width="1000" placement="right">
    <n-drawer-content :title="`${detail.theme.name} · 题材生命周期`" closable @close="closeDrawer">
      <n-spin :show="loading && !detail.theme.themeId" description="正在读取题材详情">
        <EvidenceStatusBar
            :envelope="detailResource.envelope.value"
            :error="detailResource.error.value"
            :loading="detailResource.loading.value"
            @refresh="detailResource.refresh"
        />
        <n-flex :wrap="true" class="theme-heading">
          <n-tag v-if="detail.snapshot" :type="stageType(detail.snapshot.lifecycleStage)" :bordered="false">{{ detail.snapshot.lifecycleStage }}</n-tag>
          <n-tag v-for="alias in detail.theme.aliases" :key="alias" size="small">别名：{{ alias }}</n-tag>
          <n-tag :bordered="false">催化 {{ detail.catalystSummary.total }}</n-tag>
          <n-tag type="error" :bordered="false">支持 {{ detail.catalystSummary.supports }}</n-tag>
          <n-tag type="success" :bordered="false">反驳 {{ detail.catalystSummary.contradicts }}</n-tag>
          <n-tag v-if="detail.catalystSummary.hasConflict" type="warning" :bordered="false">存在来源冲突</n-tag>
        </n-flex>
        <n-text depth="3">{{ detail.theme.description || detail.snapshot?.summary || '暂无题材说明' }}</n-text>

        <n-tabs type="line" animated display-directive="show" class="detail-tabs">
          <n-tab-pane name="lifecycle" tab="生命周期">
            <EvidenceStatusBar
                :envelope="historyResource.envelope.value"
                :error="historyResource.error.value"
                :loading="historyResource.loading.value"
                @refresh="historyResource.refresh"
            />
            <ThemeLifecycleTimeline :snapshots="snapshots" :current-snapshot="detail.snapshot"/>
          </n-tab-pane>
          <n-tab-pane name="constituents" tab="成分股">
            <n-table v-if="detail.constituents.length" striped size="small">
              <thead><tr><th>排名</th><th>证券</th><th>类型/市场</th><th>角色</th><th>贡献强度</th></tr></thead>
              <tbody>
                <tr v-for="item in detail.constituents" :key="item.constituentId || `${item.market}-${item.code}`">
                  <td>{{ item.rank || '--' }}</td>
                  <td>{{ item.name }}（{{ item.code }}）</td>
                  <td>{{ item.assetType }} / {{ item.market }}</td>
                  <td>{{ item.role || '--' }}</td>
                  <td>{{ item.contributionScore.toFixed(2) }}</td>
                </tr>
              </tbody>
            </n-table>
            <n-empty v-else description="暂无成分证券"/>
          </n-tab-pane>
          <n-tab-pane name="catalysts" tab="催化时间线">
            <EvidenceStatusBar
                :envelope="catalystResource.envelope.value"
                :error="catalystResource.error.value"
                :loading="catalystResource.loading.value"
                @refresh="catalystResource.refresh"
            />
            <n-alert v-if="catalysts.some(item => item.hasConflict)" type="warning" :bordered="false" class="conflict-alert">
              同一事件存在支持与反驳来源；页面并列保留，不替用户消解冲突。
            </n-alert>
            <n-timeline v-if="catalysts.length">
              <n-timeline-item
                  v-for="event in catalysts"
                  :key="event.catalystEventId || `${event.title}-${event.eventAt}`"
                  :type="event.hasConflict ? 'warning' : 'info'"
                  :time="`事件 ${dateTime(event.eventAt)} · 首次可用 ${dateTime(event.firstAvailableAt)}`"
                  :title="event.title"
              >
                <n-flex :wrap="true" :size="6" class="event-summary">
                  <n-tag v-if="event.eventType" size="small">{{ event.eventType }}</n-tag>
                  <n-tag v-if="event.status" size="small" :bordered="false">{{ event.status }}</n-tag>
                  <n-tag size="small" :bordered="false">事件可信度 {{ credibilityPercent(event.credibilityScore) }}</n-tag>
                  <n-tag v-if="event.hasConflict" size="small" type="warning" :bordered="false">冲突</n-tag>
                  <n-text>{{ event.summary }}</n-text>
                </n-flex>
                <n-grid :cols="1" :y-gap="7" class="source-claims">
                  <n-gi v-for="claim in event.sources" :key="claim.sourceClaimId || `${claim.sourceName}-${claim.availableAt}`">
                    <n-card size="small" :bordered="true">
                      <n-flex align="center" :wrap="true" :size="6">
                        <n-tag size="small" :type="stanceLabel(claim.stance).type" :bordered="false">{{ stanceLabel(claim.stance).label }}</n-tag>
                        <n-text strong>{{ claim.sourceName }}</n-text>
                        <n-tag size="small" :bordered="false">来源可信度 {{ credibilityPercent(claim.sourceCredibilityScore) }}</n-tag>
                        <n-button v-if="safeSourceRef(claim.sourceRef)" text tag="a" :href="safeSourceRef(claim.sourceRef)" target="_blank" rel="noopener noreferrer">原始来源</n-button>
                      </n-flex>
                      <n-text tag="div">{{ claim.summary || '无来源摘要' }}</n-text>
                      <n-text depth="3" tag="div">
                        发布 {{ dateTime(claim.publishedAt) }} · 可用 {{ dateTime(claim.availableAt) }} · 采集 {{ dateTime(claim.collectedAt) }}
                      </n-text>
                    </n-card>
                  </n-gi>
                </n-grid>
              </n-timeline-item>
            </n-timeline>
            <n-empty v-else description="暂无截止日期前可验证的催化事件"/>
          </n-tab-pane>
        </n-tabs>
      </n-spin>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.theme-heading,
.detail-tabs,
.event-summary,
.source-claims {
  margin-top: 10px;
}

.conflict-alert {
  margin: 8px 0 14px;
}
</style>
