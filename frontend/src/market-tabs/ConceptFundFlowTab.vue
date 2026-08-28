<script setup>
import FundFlowExplorer from '../components/FundFlowExplorer.vue'

defineProps({
  active: {type: Boolean, default: false},
  darkTheme: {type: Boolean, default: false},
})
</script>

<template>
  <FundFlowExplorer scope="concept" :active="active" :dark-theme="darkTheme"/>
</template>
