<script setup>
import {onMounted, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'
import {shanghaiDate} from './market-session.js'
import CurrentHotTopicsPane from './CurrentHotTopicsPane.vue'
import DailyThemesPane from './DailyThemesPane.vue'

const props = defineProps({active: {type: Boolean, default: false}})
const route = useRoute()
const router = useRouter()

function validHotView(value) {
  return value === 'daily-themes' ? 'daily-themes' : 'current'
}

const hotView = ref(validHotView(route.query.hotView))
const selectedDate = ref(String(route.query.date || shanghaiDate()))
const selectedThemeId = ref(String(route.query.themeId || ''))

function replaceQuery(values) {
  const query = {...route.query, ...values, name: '当前热门'}
  for (const key of ['themeId', 'date']) {
    if (!query[key]) delete query[key]
  }
  void router.replace({name: 'market', query})
}

function updateHotView(value) {
  hotView.value = validHotView(value)
  replaceQuery({hotView: hotView.value, date: hotView.value === 'daily-themes' ? selectedDate.value : route.query.date})
}

function updateDate(value) {
  selectedDate.value = value || shanghaiDate()
  selectedThemeId.value = ''
  replaceQuery({hotView: 'daily-themes', date: selectedDate.value, themeId: undefined})
}

function updateThemeId(value) {
  selectedThemeId.value = String(value || '')
  replaceQuery({hotView: 'daily-themes', date: selectedDate.value, themeId: selectedThemeId.value || undefined})
}

watch(() => [route.query.hotView, route.query.date, route.query.themeId], ([view, date, themeId]) => {
  hotView.value = validHotView(view)
  selectedDate.value = String(date || shanghaiDate())
  selectedThemeId.value = String(themeId || '')
})

onMounted(() => {
  if (hotView.value === 'daily-themes' && !route.query.date) replaceQuery({hotView: 'daily-themes', date: selectedDate.value})
})
</script>

<template>
  <section>
    <n-tabs :value="hotView" type="segment" animated display-directive="if" @update:value="updateHotView">
      <n-tab-pane name="current" tab="当前热门">
        <CurrentHotTopicsPane :active="active && hotView === 'current'"/>
      </n-tab-pane>
      <n-tab-pane name="daily-themes" tab="每日炒作题材">
        <DailyThemesPane
            :active="active && hotView === 'daily-themes'"
            :date="selectedDate"
            :theme-id="selectedThemeId"
            @update:date="updateDate"
            @update:theme-id="updateThemeId"
        />
      </n-tab-pane>
    </n-tabs>
  </section>
</template>
