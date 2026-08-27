<script setup>
import {defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {EventsOff, EventsOn} from '../services/browser-runtime.mjs'

const tabs = [
  {name: '股票推荐记录', component: defineAsyncComponent(() => import('./research2Recommendations.vue'))},
  {name: 'AI分析报告', component: defineAsyncComponent(() => import('./research2Report.vue'))},
  {name: '股票收益率', component: defineAsyncComponent(() => import('./research2Yield.vue'))},
  {name: '设置', component: defineAsyncComponent(() => import('./settings.vue')), props: {settingsScope: 'research2'}},
]
const route = useRoute()
const router = useRouter()
const nowTab = ref(tabs.some(tab => tab.name === route.query.name) ? String(route.query.name) : tabs[0].name)
const visited = ref([nowTab.value])

function updateTab(name) {
  if (!tabs.some(tab => tab.name === name)) return
  nowTab.value = name
  if (!visited.value.includes(name)) visited.value.push(name)
  if (route.query.name !== name) router.replace({name: 'research2', query: {...route.query, name}})
}
watch(() => route.query.name, name => updateTab(String(name || tabs[0].name)))
onMounted(() => { EventsOff('changeResearch2Tab'); EventsOn('changeResearch2Tab', msg => updateTab(msg.name)) })
onBeforeUnmount(() => EventsOff('changeResearch2Tab'))
</script>

<template>
  <n-card>
    <n-tabs type="line" animated :value="nowTab" @update-value="updateTab">
      <n-tab-pane v-for="tab in tabs" :key="tab.name" :name="tab.name" :tab="tab.name">
        <component v-if="visited.includes(tab.name)" :is="tab.component" v-bind="tab.props || {}"/>
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>
