<script setup>
import StockResearchReportList from '../components/StockResearchReportList.vue'

defineProps({
  stockCode: {
    type: String,
    default: '',
  },
})
</script>

<template>
  <StockResearchReportList :stock-code="stockCode" />
</template>
