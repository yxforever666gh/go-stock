<script setup>
import {computed, ref, toRef} from 'vue'
import EvidenceStatusBar from '../components/EvidenceStatusBar.vue'
import ThemeDetailDrawer from '../components/themes/ThemeDetailDrawer.vue'
import {
  THEME_STAGE_OPTIONS,
  heatPercent,
  stageType,
  themeListItems,
} from '../components/themes/theme-model.js'
import {useMarketDataResource} from '../composables/useMarketDataResource.js'
import {ListThemes} from '../services/themes-api.js'

const props = defineProps({
  active: {type: Boolean, default: false},
  date: {type: String, default: ''},
  themeId: {type: String, default: ''},
})
const emit = defineEmits(['update:date', 'update:theme-id'])

const active = toRef(props, 'active')
const stage = ref('')
const query = ref('')
const sort = ref('rank')
const requestKey = computed(() => ['daily-themes', props.date, stage.value, query.value.trim(), sort.value].join('|'))
const {data, envelope, error, loading, refresh} = useMarketDataResource({
  active,
  fallbackData: {tradeDate: '', items: []},
  intervalMs: 300000,
  loader: () => ListThemes({date: props.date, stage: stage.value, q: query.value.trim(), sort: sort.value, limit: 100}),
  requestKey,
  session: 'always',
})

const themes = computed(() => themeListItems(data.value))
const drawerVisible = computed({
  get: () => Boolean(props.themeId),
  set: value => { if (!value) emit('update:theme-id', '') },
})
const sortOptions = [
  {label: '按排名', value: 'rank'},
  {label: '按题材强度/热度', value: 'heat'},
  {label: '按生命周期阶段', value: 'stage'},
]

function transitionText(item) {
  if (item.stageChanged && item.previousLifecycleStage) return `${item.previousLifecycleStage} → ${item.lifecycleStage}`
  return `阶段未变 · ${item.lifecycleStage}`
}

function dateTime(value) {
  return String(value || '--').replace('T', ' ').slice(0, 19)
}
</script>

<template>
  <section class="daily-themes-pane">
    <n-flex align="center" :wrap="true" :size="8" class="themes-toolbar">
      <n-date-picker
          :formatted-value="date"
          type="date"
          value-format="yyyy-MM-dd"
          :is-date-disabled="timestamp => timestamp > Date.now()"
          style="width: 155px"
          @update:formatted-value="value => $emit('update:date', value)"
      />
      <n-select v-model:value="stage" :options="THEME_STAGE_OPTIONS" style="width: 130px" aria-label="题材阶段"/>
      <n-select v-model:value="sort" :options="sortOptions" style="width: 190px" aria-label="题材排序"/>
      <n-input v-model:value="query" clearable placeholder="搜索题材或别名" style="width: 220px"/>
      <n-tag :bordered="false" type="info">快照日期：{{ data.tradeDate || date }}</n-tag>
      <n-text depth="3">历史快照按所选交易日读取，不覆盖其他日期记录。</n-text>
    </n-flex>

    <EvidenceStatusBar :envelope="envelope" :error="error" :loading="loading" @refresh="refresh"/>
    <n-spin :show="loading && !themes.length" description="正在读取每日炒作题材">
      <n-grid v-if="themes.length" cols="1 s:2 xl:3" responsive="screen" :x-gap="12" :y-gap="12">
        <n-gi v-for="item in themes" :key="item.snapshotId || item.themeId">
          <n-card hoverable class="theme-card" @click="$emit('update:theme-id', item.themeId)">
            <template #header>
              <n-flex align="center" :wrap="false" :size="7">
                <n-tag type="error" :bordered="false">#{{ item.rank || '--' }}</n-tag>
                <n-text strong>{{ item.name }}</n-text>
                <n-tag size="small" :type="stageType(item.lifecycleStage)" :bordered="false">{{ item.lifecycleStage }}</n-tag>
              </n-flex>
            </template>
            <template #header-extra><n-tag size="small" :bordered="false">第 {{ item.cycleNo }} 轮</n-tag></template>

            <n-flex :wrap="true" :size="6" class="stage-change">
              <n-tag size="small" :type="item.stageChanged ? 'warning' : 'default'" :bordered="false">阶段变化：{{ transitionText(item) }}</n-tag>
              <n-tag v-if="item.conflictingCatalystCount" size="small" type="warning" :bordered="false">冲突催化 {{ item.conflictingCatalystCount }}</n-tag>
            </n-flex>
            <n-text depth="3">题材强度/热度 {{ heatPercent(item.heatScore).toFixed(0) }}</n-text>
            <n-progress type="line" :percentage="heatPercent(item.heatScore)" :show-indicator="false" :height="7"/>
            <n-ellipsis :line-clamp="2" class="theme-summary">{{ item.summary || '暂无当日观察摘要' }}</n-ellipsis>

            <n-flex :wrap="true" :size="5" class="representatives">
              <n-text depth="3">代表证券：</n-text>
              <n-tag v-for="security in item.representativeSecurities" :key="`${security.market}-${security.code}`" size="small">
                {{ security.name }} {{ security.code }}<template v-if="security.role"> · {{ security.role }}</template>
              </n-tag>
              <n-text v-if="!item.representativeSecurities.length" depth="3">详情中查看</n-text>
            </n-flex>
            <n-flex justify="space-between" :wrap="true" class="theme-footnote">
              <n-text depth="3">成分 {{ item.constituentCount }} · 催化 {{ item.catalystCount }}</n-text>
              <n-text depth="3">冻结 {{ dateTime(item.frozenAt || item.observedAt) }}</n-text>
            </n-flex>
          </n-card>
        </n-gi>
      </n-grid>
      <n-empty v-else-if="!loading" description="所选日期和阶段暂无题材快照" class="themes-empty"/>
    </n-spin>

    <ThemeDetailDrawer
        v-model:show="drawerVisible"
        :theme-id="themeId"
        :date="date"
    />
  </section>
</template>

<style scoped>
.themes-toolbar,
.stage-change,
.representatives,
.theme-footnote {
  margin-bottom: 9px;
}

.theme-card {
  height: 100%;
  cursor: pointer;
}

.theme-summary {
  min-height: 44px;
  margin: 9px 0;
}

.theme-footnote {
  margin-top: 10px;
  margin-bottom: 0;
}

.themes-empty {
  min-height: 320px;
  justify-content: center;
}
</style>
