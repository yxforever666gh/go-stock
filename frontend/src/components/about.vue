<script setup>
import {onMounted, ref} from 'vue'
import {GetVersionInfo} from '../services/system-api'

const version = ref('')
const buildIdentity = ref('')
const icon = ref('')

onMounted(async () => {
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
          <n-gradient-text type="info" :size="48">Stock God</n-gradient-text>
        </n-badge>
        <n-text v-if="buildIdentity" depth="3">构建标识：{{ buildIdentity }}</n-text>
        <n-alert type="info" :bordered="false">
          新版本由本机统一安装，历史记录保存在本地数据库。
        </n-alert>
        <n-button
          tag="a"
          href="https://github.com/yxforever666gh/stock-god/tags"
          target="_blank"
          type="primary"
          tertiary
        >
          查看版本记录
        </n-button>
      </n-space>

      <n-divider title-placement="center">当前能力</n-divider>
      <div class="about-copy">
        <p>Stock God 是基于 Python、Vue 3 和 SQLite 的本地股票预测工具。</p>
        <p>提供股票预测、模拟交易、收益跟踪、邮件报告与证据审计，保留独立行情数据接口。</p>
        <p>仓库由公开项目 <a href="https://github.com/ArvinLovegood/go-stock" target="_blank">ArvinLovegood/go-stock</a> 演化而来，并非原作者官方仓库。</p>
        <p><a href="https://github.com/yxforever666gh/stock-god" target="_blank">源码</a> · <a href="https://github.com/yxforever666gh/stock-god/issues" target="_blank">Issues</a></p>
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
