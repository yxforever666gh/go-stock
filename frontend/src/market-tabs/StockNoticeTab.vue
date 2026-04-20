<script setup>
import StockNoticeList from '../components/StockNoticeList.vue'

defineProps({
  stockCode: {
    type: String,
    default: '',
  },
})
</script>

<template>
  <StockNoticeList :stock-code="stockCode" />
</template>
