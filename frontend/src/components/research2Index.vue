<script setup>
import {defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch} from 'vue'
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
    <n-tabs v-if="nowTab !== '设置'" type="card" :value="selectedSlot" @update:value="updateSlot" style="margin-bottom:16px">
      <n-tab v-for="slot in RESEARCH2_SLOTS" :key="slot.value" :name="slot.value">{{slot.label}}</n-tab>
    </n-tabs>
    <n-text v-if="nowTab !== '设置'" depth="3">{{ selectedSlot }} 独立账户 · 今日定时卖出：{{ slotStates.find(item => item.slot === selectedSlot)?.sellCompletedAt ? '已完成' : '尚未完成' }}</n-text>
    <n-tabs type="line" animated :value="nowTab" @update-value="updateTab">
      <n-tab-pane v-for="tab in tabs" :key="tab.name" :name="tab.name" :tab="tab.name">
        <component v-if="visited.includes(tab.name)" :is="tab.component" :key="tab.name === '设置' ? tab.name : `${tab.name}:${selectedSlot}`" :slot="selectedSlot" v-bind="tab.props || {}"/>
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>
