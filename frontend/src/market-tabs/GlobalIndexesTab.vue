<script setup>
import KLineChart from '../components/KLineChart.vue'

defineProps({
  globalStockIndexes: {
    type: Object,
    default: () => ({}),
  },
  panelHeight: {
    type: Number,
    default: 500,
  },
})

function getAreaName(code) {
  switch (code) {
    case 'america':
      return '美洲'
    case 'europe':
      return '欧洲'
    case 'asia':
      return '亚洲'
    case 'common':
      return '常用'
    case 'other':
      return '其他'
    default:
      return code
  }
}
</script>

<template>
  <n-tabs type="segment" animated>
    <n-tab-pane name="全球指数" tab="全球指数">
      <n-grid :cols="5" :y-gap="0">
        <n-gi v-for="(val, key) in globalStockIndexes" :key="key">
          <n-list bordered>
            <template #header>
              {{ getAreaName(key) }}
            </template>
            <n-list-item v-for="item in val" :key="item.code">
              <n-grid :cols="3" :y-gap="0">
                <n-gi>
                  <n-text :type="item.zdf > 0 ? 'error' : 'success'">
                    <n-image :src="item.img" width="20" /> &nbsp;{{ item.name }}
                  </n-text>
                </n-gi>
                <n-gi>
                  <n-text :type="item.zdf > 0 ? 'error' : 'success'">{{ item.zxj }}</n-text>&nbsp;
                  <n-text :type="item.zdf > 0 ? 'error' : 'success'">
                    <n-number-animation :precision="2" :from="0" :to="item.zdf" />
                    %
                  </n-text>
                </n-gi>
                <n-gi>
                  <n-text :type="item.state === 'open' ? 'success' : 'warning'">
                    {{ item.state === 'open' ? '开市' : '休市' }}
                  </n-text>
                </n-gi>
              </n-grid>
            </n-list-item>
          </n-list>
        </n-gi>
      </n-grid>
    </n-tab-pane>
    <n-tab-pane name="上证指数" tab="上证指数">
      <KLineChart code="sh000001" :chart-height="panelHeight" stockName="上证指数" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="深证成指" tab="深证成指">
      <KLineChart code="sz399001" :chart-height="panelHeight" stockName="深证成指" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="创业板指" tab="创业板指">
      <KLineChart code="sz399006" :chart-height="panelHeight" stockName="创业板指" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="恒生指数" tab="恒生指数">
      <KLineChart code="hkHSI" :chart-height="panelHeight" stockName="恒生指数" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="纳斯达克" tab="纳斯达克">
      <KLineChart code="us.IXIC" :chart-height="panelHeight" stockName="纳斯达克" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="道琼斯" tab="道琼斯">
      <KLineChart code="us.DJI" :chart-height="panelHeight" stockName="道琼斯" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
    <n-tab-pane name="标普500" tab="标普500">
      <KLineChart code="us.INX" :chart-height="panelHeight" stockName="标普500" :k-days="20" :dark-theme="true" />
    </n-tab-pane>
  </n-tabs>
</template>
