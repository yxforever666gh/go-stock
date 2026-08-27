<script setup>
import { defineAsyncComponent, nextTick, onBeforeMount, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EventsOff, EventsOn } from "../services/browser-runtime.mjs";
import { useRoute, useRouter } from 'vue-router'

const TAB_ORDER_STORAGE_KEY = 'research-index-tab-order-v163'
const ResearchYield = defineAsyncComponent(() => import('./researchYield.vue'))
const ResearchReport = defineAsyncComponent(() => import('./researchReport.vue'))
const ResearchRecommendations = defineAsyncComponent(() => import('./researchRecommendations.vue'))
const Settings = defineAsyncComponent(() => import('./settings.vue'))
const defaultTabs = [
  { name: "股票推荐记录", component: ResearchRecommendations },
  { name: "AI分析报告", component: ResearchReport },
  { name: "股票收益率", component: ResearchYield },
	{ name: "设置", component: Settings },
]

const tabs = ref([...defaultTabs])
const nowTab = ref("股票推荐记录")
const route = useRoute()
const router = useRouter()
const cardRef = ref(null)
const dragSourceName = ref("")
const dragTargetName = ref("")
const visitedTabs = ref([])

function markTabVisited(name) {
  if (!name || visitedTabs.value.includes(name)) {
    return
  }
  visitedTabs.value = [...visitedTabs.value, name]
}

function shouldRenderTab(name) {
  return visitedTabs.value.includes(name)
}

function loadSavedTabOrder() {
  const savedOrderText = window.localStorage.getItem(TAB_ORDER_STORAGE_KEY)
  if (!savedOrderText) {
    return [...defaultTabs]
  }

  try {
    const savedOrder = JSON.parse(savedOrderText)
    if (!Array.isArray(savedOrder)) {
      return [...defaultTabs]
    }

    const tabMap = new Map(defaultTabs.map((tab) => [tab.name, tab]))
    const orderedTabs = savedOrder
        .map((name) => tabMap.get(String(name)))
        .filter(Boolean)
    const missingTabs = defaultTabs.filter((tab) => !savedOrder.includes(tab.name))
    return [...orderedTabs, ...missingTabs]
  } catch (error) {
    console.warn('解析研究页标签顺序失败:', error)
    return [...defaultTabs]
  }
}

function persistTabOrder() {
  window.localStorage.setItem(
      TAB_ORDER_STORAGE_KEY,
      JSON.stringify(tabs.value.map((tab) => tab.name))
  )
}

function updateTab(name) {
  if (tabs.value.some((tab) => tab.name === name)) {
    nowTab.value = name
    markTabVisited(name)
    if (String(route.query.name || "") !== name) {
      router.replace({
        name: 'research',
        query: {
          ...route.query,
          name,
        },
      })
    }
  }
}

function moveTab(sourceName, targetName) {
  const sourceIndex = tabs.value.findIndex((tab) => tab.name === sourceName)
  const targetIndex = tabs.value.findIndex((tab) => tab.name === targetName)
  if (sourceIndex === -1 || targetIndex === -1 || sourceIndex === targetIndex) {
    return
  }

  const nextTabs = [...tabs.value]
  const [movedTab] = nextTabs.splice(sourceIndex, 1)
  nextTabs.splice(targetIndex, 0, movedTab)
  tabs.value = nextTabs
  persistTabOrder()
}

function clearDragStyles() {
  const tabElements = cardRef.value?.$el?.querySelectorAll('.n-tabs-tab') || []
  tabElements.forEach((tab) => {
    tab.classList.remove('tab-drag-over', 'tab-dragging')
  })
}

function handleTabDragStart(event, name) {
  dragSourceName.value = name
  dragTargetName.value = name
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', name)
  event.currentTarget?.classList?.add('tab-dragging')
}

function handleTabDragOver(event) {
  event.preventDefault()
  event.dataTransfer.dropEffect = 'move'
}

function handleTabDragEnter(event, name) {
  event.preventDefault()
  dragTargetName.value = name
  const tabElement = event.currentTarget
  tabElement?.classList?.add('tab-drag-over')
}

function handleTabDragLeave(event) {
  event.currentTarget?.classList?.remove('tab-drag-over')
}

function handleTabDrop(event, name) {
  event.preventDefault()
  const sourceName = dragSourceName.value || event.dataTransfer.getData('text/plain')
  moveTab(sourceName, name)
  clearDragStyles()
  dragSourceName.value = ""
  dragTargetName.value = ""
}

function handleTabDragEnd() {
  clearDragStyles()
  dragSourceName.value = ""
  dragTargetName.value = ""
}

function cleanupDraggableTabs() {
  const tabElements = cardRef.value?.$el?.querySelectorAll('.n-tabs-tab') || []
  tabElements.forEach((tab) => {
    const dragHandlers = tab.__researchDragHandlers
    if (!dragHandlers) {
      tab.removeAttribute('draggable')
      return
    }

    tab.removeEventListener('dragstart', dragHandlers.dragstart)
    tab.removeEventListener('dragover', dragHandlers.dragover)
    tab.removeEventListener('dragenter', dragHandlers.dragenter)
    tab.removeEventListener('dragleave', dragHandlers.dragleave)
    tab.removeEventListener('drop', dragHandlers.drop)
    tab.removeEventListener('dragend', dragHandlers.dragend)
    delete tab.__researchDragHandlers
    tab.removeAttribute('draggable')
  })
}

function initDraggableTabs() {
  cleanupDraggableTabs()
  nextTick(() => {
    const tabElements = cardRef.value?.$el?.querySelectorAll('.n-tabs-tab') || []
    tabElements.forEach((tab) => {
      const name = tab.getAttribute('data-name')
      if (!name) {
        return
      }

      const dragHandlers = {
        dragstart: (event) => handleTabDragStart(event, name),
        dragover: handleTabDragOver,
        dragenter: (event) => handleTabDragEnter(event, name),
        dragleave: handleTabDragLeave,
        drop: (event) => handleTabDrop(event, name),
        dragend: handleTabDragEnd,
      }

      tab.__researchDragHandlers = dragHandlers
      tab.setAttribute('draggable', 'true')
      tab.addEventListener('dragstart', dragHandlers.dragstart)
      tab.addEventListener('dragover', dragHandlers.dragover)
      tab.addEventListener('dragenter', dragHandlers.dragenter)
      tab.addEventListener('dragleave', dragHandlers.dragleave)
      tab.addEventListener('drop', dragHandlers.drop)
      tab.addEventListener('dragend', dragHandlers.dragend)
    })
  })
}

onBeforeMount(() => {
  tabs.value = loadSavedTabOrder()
  const tabName = String(route.query.name || "")
  if (tabs.value.some((tab) => tab.name === tabName)) {
    nowTab.value = tabName
  }
  markTabVisited(nowTab.value)
})

onMounted(() => {
  initDraggableTabs()

  // Clean up any stale event listeners (defensive)
  EventsOff("changeResearchTab")

  // Register event listener
  EventsOn("changeResearchTab", async (msg) => {
    updateTab(msg.name)
  })
})

watch(tabs, () => {
  initDraggableTabs()
}, { deep: true })

onBeforeUnmount(() => {
  cleanupDraggableTabs()
  EventsOff("changeResearchTab")
})
</script>

<template>
  <n-card ref="cardRef">
    <n-tabs type="line" animated @update-value="updateTab" :value="nowTab">
      <n-tab-pane
          v-for="tab in tabs"
          :key="tab.name"
          :name="tab.name"
          :tab="tab.name"
      >
        <component v-if="shouldRenderTab(tab.name)" :is="tab.component"/>
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>

<style scoped>
:deep(.n-tabs-nav .n-tabs-tab) {
  position: relative;
}

:deep(.n-tabs-nav .n-tabs-tab[draggable="true"]) {
  user-select: none;
  cursor: move;
}

:deep(.n-tabs-nav .n-tabs-tab.tab-drag-over) {
  background-color: #edf6ff;
  border-radius: 10px;
  box-shadow: inset 0 0 0 1px #3b82f6;
  transform: translateY(-1px);
  transition: background-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

:deep(.n-tabs-nav .n-tabs-tab.tab-dragging) {
  opacity: 0.55;
}
</style>
