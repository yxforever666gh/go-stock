<script setup>
import RankTable from '../components/rankTable.vue'
</script>

<template>
  <n-tabs type="card" animated>
    <n-tab-pane name="netamount" tab="净流入额排名">
      <RankTable :header-title="'净流入额排名'" :sort="'netamount'" />
    </n-tab-pane>
    <n-tab-pane name="outamount" tab="流出资金排名">
      <RankTable :header-title="'流出资金排名'" :sort="'outamount'" />
    </n-tab-pane>
    <n-tab-pane name="ratioamount" tab="净流入率排名">
      <RankTable :header-title="'净流入率排名'" :sort="'ratioamount'" />
    </n-tab-pane>
    <n-tab-pane name="r0_net" tab="主力净流入额排名">
      <RankTable :header-title="'主力净流入额排名'" :sort="'r0_net'" />
    </n-tab-pane>
    <n-tab-pane name="r0_out" tab="主力流出排名">
      <RankTable :header-title="'主力流出排名'" :sort="'r0_out'" />
    </n-tab-pane>
    <n-tab-pane name="r0_ratio" tab="主力净流入率排名">
      <RankTable :header-title="'主力净流入率排名'" :sort="'r0_ratio'" />
    </n-tab-pane>
    <n-tab-pane name="r3_net" tab="散户净流入额排名">
      <RankTable :header-title="'散户净流入额排名'" :sort="'r3_net'" />
    </n-tab-pane>
    <n-tab-pane name="r3_out" tab="散户流出排名">
      <RankTable :header-title="'散户流出排名'" :sort="'r3_out'" />
    </n-tab-pane>
    <n-tab-pane name="r3_ratio" tab="散户净流入率排名">
      <RankTable :header-title="'散户净流入率排名'" :sort="'r3_ratio'" />
    </n-tab-pane>
  </n-tabs>
</template>
