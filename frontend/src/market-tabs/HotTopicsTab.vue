<script setup>
import HotStockList from '../components/HotStockList.vue'
import HotTopics from '../components/HotTopics.vue'
import InvestCalendarTimeLine from '../components/InvestCalendarTimeLine.vue'
import ClsCalendarTimeLine from '../components/ClsCalendarTimeLine.vue'
</script>

<template>
  <n-tabs type="card" animated>
    <n-tab-pane name="全球" tab="全球">
      <HotStockList :market-type="'10'" />
    </n-tab-pane>
    <n-tab-pane name="沪深" tab="沪深">
      <HotStockList :market-type="'12'" />
    </n-tab-pane>
    <n-tab-pane name="港股" tab="港股">
      <HotStockList :market-type="'13'" />
    </n-tab-pane>
    <n-tab-pane name="美股" tab="美股">
      <HotStockList :market-type="'11'" />
    </n-tab-pane>
    <n-tab-pane name="热门话题" tab="热门话题">
      <n-grid :cols="1" :y-gap="10">
        <n-grid-item>
          <HotTopics />
        </n-grid-item>
      </n-grid>
    </n-tab-pane>
    <n-tab-pane name="重大事件时间轴" tab="重大事件时间轴">
      <InvestCalendarTimeLine />
    </n-tab-pane>
    <n-tab-pane name="财经日历" tab="财经日历">
      <ClsCalendarTimeLine />
    </n-tab-pane>
  </n-tabs>
</template>
