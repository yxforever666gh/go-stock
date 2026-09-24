<script setup>
defineProps({count: Number, hasMore: Boolean, loading: Boolean, error: String})
defineEmits(['load-more'])
</script>

<template>
  <n-alert v-if="error" type="error" :bordered="false">{{ error }}</n-alert>
  <n-flex justify="space-between" align="center">
    <n-text depth="3">已加载 {{ count }} 条；刷新返回最新记录。</n-text>
    <n-button v-if="hasMore" :loading="loading" @click="$emit('load-more')">{{ error ? '重试加载' : '加载更多' }}</n-button>
    <n-text v-else depth="3">已加载全部记录</n-text>
  </n-flex>
</template>
