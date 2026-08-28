<script setup>
import ProfessionalKLinePanel from './chart/ProfessionalKLinePanel.vue'

defineProps({
  code: {type: String, default: ''},
  stockName: {type: String, default: ''},
  assetType: {type: String, default: 'stock'},
  market: {type: String, default: ''},
  period: {type: String, default: 'day'},
  adjustment: {type: String, default: ''},
  kDays: {type: Number, default: 14},
  chartHeight: {type: Number, default: 500},
  darkTheme: {type: Boolean, default: false},
})
</script>

<template>
  <ProfessionalKLinePanel
      :code="code"
      :name="stockName"
      :asset-type="assetType"
      :market="market"
      :initial-period="period"
      :initial-adjustment="adjustment"
      :initial-visible-bars="Math.max(20, kDays)"
      :chart-height="chartHeight"
      :dark-theme="darkTheme"
  />
</template>
