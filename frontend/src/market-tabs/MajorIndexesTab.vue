<script setup>
import KLineChart from '../components/KLineChart.vue'

defineProps({
  panelHeight: {
    type: Number,
    default: 500,
  },
})
</script>

<template>
  <n-tabs type="segment" animated>
    <n-tab-pane name="恒生科技指数" tab="恒生科技指数">
      <KLineChart code="hkHSTECH" :chart-height="panelHeight" stockName="恒生科技指数" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="科创50" tab="科创50">
      <KLineChart code="sh000688" :chart-height="panelHeight" stockName="科创50" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="科创芯片" tab="科创芯片">
      <KLineChart code="sh000685" :chart-height="panelHeight" stockName="科创芯片" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="证券龙头" tab="证券龙头">
      <KLineChart code="sz399437" :chart-height="panelHeight" stockName="证券龙头" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="高端装备" tab="高端装备">
      <KLineChart code="sz399437" :chart-height="panelHeight" stockName="高端装备" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="中证银行" tab="中证银行">
      <KLineChart code="sz399986" :chart-height="panelHeight" stockName="中证银行" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="上证医药" tab="上证医药">
      <KLineChart code="sh000037" :chart-height="panelHeight" stockName="上证医药" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="沪深300" tab="沪深300">
      <KLineChart code="sh000300" :chart-height="panelHeight" stockName="沪深300" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="上证50" tab="上证50">
      <KLineChart code="sh000016" :chart-height="panelHeight" stockName="上证50" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="中证A500" tab="中证A500">
      <KLineChart code="sh000510" :chart-height="panelHeight" stockName="中证A500" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="中证1000" tab="中证1000">
      <KLineChart code="sh000852" :chart-height="panelHeight" stockName="中证1000" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="中证白酒" tab="中证白酒">
      <KLineChart code="sz399997" :chart-height="panelHeight" stockName="中证白酒" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="富时中国三倍做多" tab="富时中国三倍做多">
      <KLineChart code="usYINN.AM" :chart-height="panelHeight" stockName="富时中国三倍做多" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="VIX恐慌指数" tab="VIX恐慌指数">
      <KLineChart code="usUVXY.AM" :chart-height="panelHeight" stockName="VIX恐慌指数" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
  </n-tabs>
</template>
