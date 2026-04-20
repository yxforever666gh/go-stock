<script setup>
import AnalyzeMartket from '../components/AnalyzeMartket.vue'
import NewsList from '../components/newsList.vue'

defineProps({
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
      <AnalyzeMartket :dark-theme="darkTheme" :chart-height="300" :kDays="1" :name="'最近24小时热词'" />
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
