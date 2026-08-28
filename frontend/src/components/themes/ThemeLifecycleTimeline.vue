<script setup>
import {computed} from 'vue'
import {THEME_STAGES, heatPercent, normalizeThemeSnapshot, stageType, themeSnapshots} from './theme-model.js'

const props = defineProps({
  snapshots: {type: Array, default: () => []},
  currentSnapshot: {type: Object, default: null},
})

const rows = computed(() => {
  const history = themeSnapshots({items: props.snapshots})
  if (!props.currentSnapshot) return history
  const current = normalizeThemeSnapshot(props.currentSnapshot)
  const withoutDuplicate = history.filter(item => item.snapshotId !== current.snapshotId && item.tradeDate !== current.tradeDate)
  return [...withoutDuplicate, current].sort((left, right) => left.tradeDate.localeCompare(right.tradeDate))
})
const currentStage = computed(() => rows.value.at(-1)?.lifecycleStage || props.currentSnapshot?.lifecycleStage || '观察')
const currentStep = computed(() => Math.max(1, THEME_STAGES.indexOf(currentStage.value) + 1))
const transitions = computed(() => rows.value.map((item, index) => ({
  ...item,
  previousStage: index > 0 ? rows.value[index - 1].lifecycleStage : '',
  changed: index > 0 && rows.value[index - 1].lifecycleStage !== item.lifecycleStage,
})).reverse())
</script>

<template>
  <section>
    <n-steps :current="currentStep" status="process" size="small" class="lifecycle-steps">
      <n-step v-for="stage in THEME_STAGES" :key="stage" :title="stage"/>
    </n-steps>
    <n-timeline v-if="transitions.length">
      <n-timeline-item
          v-for="item in transitions"
          :key="item.snapshotId || item.tradeDate"
          :type="stageType(item.lifecycleStage)"
          :time="item.tradeDate"
          :title="item.changed ? `${item.previousStage} → ${item.lifecycleStage}` : item.lifecycleStage"
      >
        <n-flex :wrap="true" :size="6">
          <n-tag size="small" :bordered="false">第 {{ item.cycleNo }} 轮</n-tag>
          <n-tag size="small" :bordered="false">排名 {{ item.rank || '--' }}</n-tag>
          <n-tag size="small" :bordered="false">题材强度/热度 {{ heatPercent(item.heatScore).toFixed(0) }}</n-tag>
          <n-text depth="3">{{ item.summary || '无补充摘要' }}</n-text>
        </n-flex>
      </n-timeline-item>
    </n-timeline>
    <n-empty v-else description="暂无生命周期快照"/>
  </section>
</template>

<style scoped>
.lifecycle-steps {
  margin: 8px 0 22px;
}
</style>
