<script setup>
import MarketHotWords from '../components/MarketHotWords.vue'
import NewsList from '../components/newsList.vue'
import MarketBreadthOverview from '../components/MarketBreadthOverview.vue'

defineProps({
  active: {
    type: Boolean,
    default: false,
  },
  darkTheme: {
    type: Boolean,
    default: false,
  },
  telegraphList: {
    type: Array,
    default: () => [],
  },
  sinaNewsList: {
    type: Array,
    default: () => [],
  },
  foreignNewsList: {
    type: Array,
    default: () => [],
  },
})

const emit = defineEmits(['refresh'])

function handleRefresh(source) {
  emit('refresh', source)
}
</script>

<template>
  <n-grid :cols="1" :y-gap="0">
    <n-gi>
      <MarketBreadthOverview :active="active" />
    </n-gi>
    <n-gi>
      <MarketHotWords :active="active" :dark-theme="darkTheme" />
    </n-gi>
    <n-gi>
      <n-grid :cols="foreignNewsList.length ? 3 : 2" :y-gap="0">
        <n-gi>
          <NewsList :news-list="telegraphList" :header-title="'财联社电报'" @update:message="handleRefresh" />
        </n-gi>
        <n-gi>
          <NewsList :news-list="sinaNewsList" :header-title="'新浪财经'" @update:message="handleRefresh" />
        </n-gi>
        <n-gi v-if="foreignNewsList.length > 0">
          <NewsList :news-list="foreignNewsList" :header-title="'外媒'" @update:message="handleRefresh" />
        </n-gi>
      </n-grid>
    </n-gi>
  </n-grid>
</template>
