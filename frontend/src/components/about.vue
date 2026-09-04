<script setup>
import {onMounted, ref} from 'vue'
import {GetVersionInfo} from '../services/system-api'

const version = ref('')
const buildIdentity = ref('')
const icon = ref('')

onMounted(async () => {
  document.title = '关于软件'
  const info = await GetVersionInfo()
  version.value = info?.version || 'dev'
  buildIdentity.value = info?.content || ''
  icon.value = info?.icon || ''
})
</script>

<template>
  <n-space vertical size="large">
    <n-card size="large">
      <n-divider title-placement="center">关于软件</n-divider>
      <n-space vertical align="center">
        <n-image width="100" :src="icon"/>
        <n-badge :value="version" :offset="[70, 6]" type="success">
          <n-gradient-text type="info" :size="48">go-stock</n-gradient-text>
        </n-badge>
        <n-text v-if="buildIdentity" depth="3">构建标识：{{ buildIdentity }}</n-text>
        <n-alert type="info" :bordered="false">
          应用内自动更新已经移除。新版本通过本地 release/deploy 流程安装，历史数据保留在独立数据库中。
        </n-alert>
        <n-button
          tag="a"
          href="https://github.com/yxforever666gh/go-stock/releases"
          target="_blank"
          type="primary"
          tertiary
        >
          查看 Releases
        </n-button>
      </n-space>

      <n-divider title-placement="center">当前能力</n-divider>
      <div class="about-copy">
        <p>go-stock 是基于 Go、Vue 3、SQLite 和 Naive UI 的本地股票行情与 AI 研究工作台。</p>
        <p>支持股票自选、市场数据、基金、研究中心、模拟交易、收益跟踪、邮件报告和运行时任务管理。</p>
        <p>仓库由公开项目 <a href="https://github.com/ArvinLovegood/go-stock" target="_blank">ArvinLovegood/go-stock</a> 演化而来，并非原作者官方仓库。</p>
        <p><a href="https://github.com/yxforever666gh/go-stock" target="_blank">源码</a> · <a href="https://github.com/yxforever666gh/go-stock/issues" target="_blank">Issues</a></p>
        <p class="warning">本软件仅供学习研究，AI 分析结果不构成任何投资建议或决策依据。</p>
      </div>
    </n-card>
  </n-space>
</template>

<style scoped>
.about-copy {
  max-width: 920px;
  margin: 0 auto;
  line-height: 1.7;
}

.about-copy p {
  margin: 4px 0;
}

.about-copy a {
  color: #18a058;
  text-decoration: none;
}

.warning {
  color: crimson;
}
</style>
