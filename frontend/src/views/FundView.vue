<script setup>
import {defineAsyncComponent, ref, watch} from 'vue'
import {useRoute, useRouter} from 'vue-router'

const FundWatchlist = defineAsyncComponent(() => import('../components/fund.vue'))
const FundRankings = defineAsyncComponent(() => import('../components/funds/FundRankingsPane.vue'))
const route = useRoute()
const router = useRouter()
const activeView = ref(route.query.fundView === 'rankings' ? 'rankings' : 'watchlist')

function updateView(value) {
  activeView.value = value === 'rankings' ? 'rankings' : 'watchlist'
  if (route.query.fundView === activeView.value) return
  const query = {...route.query, fundView: activeView.value}
  if (activeView.value === 'watchlist') delete query.etfCode
  router.replace({name: 'fund', query})
}

watch(() => route.query.fundView, value => {
  const next = value === 'rankings' ? 'rankings' : 'watchlist'
  if (activeView.value !== next) activeView.value = next
})
</script>

<template>
  <n-card>
    <n-tabs :value="activeView" type="line" animated display-directive="if" @update:value="updateView">
      <n-tab-pane name="watchlist" tab="基金自选"><FundWatchlist/></n-tab-pane>
      <n-tab-pane name="rankings" tab="基金排行"><FundRankings/></n-tab-pane>
    </n-tabs>
  </n-card>
</template>
