<script setup>
import {defineAsyncComponent, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'

const FundRankingList = defineAsyncComponent(() => import('./FundRankingList.vue'))
const ETFRankingList = defineAsyncComponent(() => import('./ETFRankingList.vue'))
const route = useRoute()
const router = useRouter()
const activeRanking = ref(route.query.rankingView === 'etfs' ? 'etfs' : 'funds')

function updateRanking(value) {
  activeRanking.value = value === 'etfs' ? 'etfs' : 'funds'
  if (route.query.rankingView === activeRanking.value) return
  const query = {...route.query, fundView: 'rankings', rankingView: activeRanking.value}
  if (activeRanking.value === 'funds') delete query.etfCode
  router.replace({name: 'fund', query})
}

watch(() => route.query.rankingView, value => {
  const next = value === 'etfs' ? 'etfs' : 'funds'
  if (activeRanking.value !== next) activeRanking.value = next
})
</script>

<template>
  <n-tabs :value="activeRanking" type="segment" animated display-directive="if" @update:value="updateRanking">
    <n-tab-pane name="funds" tab="场外基金排行"><FundRankingList/></n-tab-pane>
    <n-tab-pane name="etfs" tab="场内 ETF"><ETFRankingList/></n-tab-pane>
  </n-tabs>
</template>
