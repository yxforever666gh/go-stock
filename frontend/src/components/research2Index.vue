<script setup>
import {computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {RESEARCH2_SLOTS, validResearch2Slot} from '../utils/research2-slots.js'
import {usePolling} from '../composables/usePolling.js'
import {ListResearch2Slots} from '../services/research2-api'
import {EventsOff, EventsOn} from '../services/browser-runtime.mjs'

const tabs = [
  {name: '股票推荐记录', component: defineAsyncComponent(() => import('./research2Recommendations.vue'))},
  {name: 'AI分析报告', component: defineAsyncComponent(() => import('./research2Report.vue'))},
  {name: '股票收益率', component: defineAsyncComponent(() => import('./research2Yield.vue'))},
  {name: '设置', component: defineAsyncComponent(() => import('./settings.vue')), props: {settingsScope: 'research2'}},
]
const route = useRoute()
const router = useRouter()
const selectedSlot = ref(validResearch2Slot(route.query.slot) ? String(route.query.slot) : '09:50')
const slotStates = ref([])
async function refreshSlots() { try { slotStates.value = await ListResearch2Slots() || [] } catch { slotStates.value = [] } }
const slotPolling = usePolling(refreshSlots, 15000)
const selectedSlotInfo = computed(() => RESEARCH2_SLOTS.find(slot => slot.value === selectedSlot.value) || RESEARCH2_SLOTS[0])
const selectedSlotState = computed(() => slotStates.value.find(item => item.slot === selectedSlot.value))
const slotOptions = computed(() => RESEARCH2_SLOTS.map(slot => {
 const state = slotStates.value.find(item => item.slot === slot.value)
 return {key: slot.value, label: `${slot.label} · ${state?.sellCompletedAt ? '已完成' : '尚未完成'}`}
}))
function updateSlot(slot) {
 if (!validResearch2Slot(slot)) return
 selectedSlot.value = slot
 if (route.query.slot !== slot) router.replace({name: 'research2', query: {...route.query, slot}})
 void refreshSlots()
}
watch(() => route.query.slot, slot => updateSlot(validResearch2Slot(slot) ? String(slot) : '09:50'))
const nowTab = ref(tabs.some(tab => tab.name === route.query.name) ? String(route.query.name) : tabs[0].name)
const visited = ref([nowTab.value])

function updateTab(name) {
  if (!tabs.some(tab => tab.name === name)) return
  nowTab.value = name
  if (!visited.value.includes(name)) visited.value.push(name)
  if (route.query.name !== name) router.replace({name: 'research2', query: {...route.query, name}})
}
watch(() => route.query.name, name => updateTab(String(name || tabs[0].name)))
onMounted(() => { slotPolling.start({immediate: true}); EventsOff('changeResearch2Tab'); EventsOn('changeResearch2Tab', msg => updateTab(msg.name)) })
onBeforeUnmount(() => EventsOff('changeResearch2Tab'))
</script>

<template>
  <n-card>
    <div v-if="nowTab !== '设置'" class="research2-slot-toolbar">
      <n-dropdown trigger="click" :options="slotOptions" @select="updateSlot">
        <n-button secondary size="small" aria-label="选择五分钟区间">
          {{ selectedSlotInfo.label }}
          <span class="research2-slot-chevron" aria-hidden="true">⌄</span>
        </n-button>
      </n-dropdown>
      <n-text depth="3">独立账户 · 今日定时卖出：{{ selectedSlotState?.sellCompletedAt ? '已完成' : '尚未完成' }}</n-text>
    </div>
    <n-tabs type="line" animated :value="nowTab" @update-value="updateTab">
      <n-tab-pane v-for="tab in tabs" :key="tab.name" :name="tab.name" :tab="tab.name">
        <component v-if="visited.includes(tab.name)" :is="tab.component" :key="tab.name === '设置' ? tab.name : `${tab.name}:${selectedSlot}`" :slot="selectedSlot" v-bind="tab.props || {}"/>
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>

<style scoped>
.research2-slot-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 34px;
  margin-bottom: 16px;
}

.research2-slot-chevron {
  margin-left: 8px;
  font-size: 14px;
  line-height: 1;
}
</style>
