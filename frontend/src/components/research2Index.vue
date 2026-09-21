<script setup>
import {computed, defineAsyncComponent, h, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {RESEARCH2_SLOTS, research2SlotBuyLabel, validResearch2Slot} from '../utils/research2-slots.js'
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
 return {key: slot.value, label: slot.label, summary: research2SlotBuyLabel(state)}
}))
function renderSlotLabel(option) {
 const current = option.key === selectedSlot.value
 return h('div', {class: 'research2-slot-option-label'}, [
  h('span', {class: 'research2-slot-option-title'}, String(option.label)),
  current ? h('span', {class: 'research2-slot-current-mark'}, '当前') : null,
  h('span', {class: 'research2-slot-option-summary'}, String(option.summary || '买入：等待报告')),
 ])
}
function slotNodeProps(option) {
 const current = option.key === selectedSlot.value
 return current ? {class: 'research2-slot-option-current', 'aria-current': 'true'} : {'aria-current': 'false'}
}
function buyTagType(state) {
 switch (state?.buyStatus) {
  case 'bought_full': return 'success'
  case 'bought_partial': return 'warning'
  case 'awaiting_quote', 'processing': return 'info'
  case 'cutoff', 'failed': return 'error'
  case 'disabled', 'no_recommendation', 'no_purchase': return 'default'
  default: return 'default'
 }
}
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
      <n-dropdown trigger="click" :options="slotOptions" :render-label="renderSlotLabel" :node-props="slotNodeProps" @select="updateSlot">
        <n-button secondary size="small" class="research2-slot-current-button" aria-label="选择五分钟区间">
          <span class="research2-slot-current-prefix">当前选择</span>
          <strong>{{ selectedSlotInfo.label }}</strong>
          <span class="research2-slot-chevron" aria-hidden="true">⌄</span>
        </n-button>
      </n-dropdown>
      <div class="research2-slot-status" aria-live="polite">
        <n-tag size="small" :type="buyTagType(selectedSlotState)" bordered="false">{{ research2SlotBuyLabel(selectedSlotState) }}</n-tag>
      </div>
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
  flex-wrap: wrap;
  gap: 12px;
  min-height: 34px;
  margin-bottom: 16px;
}

.research2-slot-current-button {
  border-color: #18a058 !important;
  background: linear-gradient(135deg, #ecf9f0, #f8fffa) !important;
  box-shadow: 0 5px 14px rgba(24, 160, 88, .2);
  color: #087443;
  font-weight: 700;
}

.research2-slot-current-prefix {
  margin-right: 6px;
  color: #18a058;
  font-size: 12px;
  font-weight: 700;
}

.research2-slot-status {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.research2-slot-chevron {
  margin-left: 8px;
  font-size: 14px;
  line-height: 1;
}

:global(.research2-slot-option-current .n-dropdown-option-body) {
  background: linear-gradient(90deg, #e9f8ee, #f9fffb);
  box-shadow: inset 3px 0 #18a058, 0 4px 12px rgba(24, 160, 88, .14);
  font-weight: 700;
}

.research2-slot-option-label {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 2px 8px;
  min-width: 168px;
}

.research2-slot-option-title {
  font-weight: 600;
}

.research2-slot-current-mark {
  align-self: center;
  border-radius: 999px;
  background: #18a058;
  color: #fff;
  font-size: 11px;
  line-height: 18px;
  padding: 0 6px;
}

.research2-slot-option-summary {
  grid-column: 1 / -1;
  color: #7a7f87;
  font-size: 12px;
  font-weight: 400;
}
</style>
