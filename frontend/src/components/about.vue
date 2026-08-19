<script setup>
import { h, onBeforeUnmount, onMounted, ref } from 'vue';
import { CheckUpdate, GetVersionInfo } from '../services/system-api';
import { BrowserOpenURL as OpenURL, Environment, EventsOff, EventsOn } from '../services/browser-runtime.mjs';
import { NAvatar, NButton, useNotification } from 'naive-ui';

const updateLog = ref('');
const versionInfo = ref('');
const icon = ref('');
const officialStatement = ref('');
const notify = useNotification();
const selfUpdateEnabled = ref(true);
const manualUpdateHint = ref('');

onMounted(() => {
  document.title = '关于软件';
  GetVersionInfo().then((res) => {
    updateLog.value = res.content;
    versionInfo.value = res.version;
    icon.value = res.icon;
    officialStatement.value = res.officialStatement || '';
    selfUpdateEnabled.value = res.selfUpdateEnabled !== false;
    manualUpdateHint.value = res.manualUpdateHint || '';
  });

  // Clean up any stale event listeners (defensive)
  EventsOff('updateVersion');

  // Register event listener
  EventsOn('updateVersion', async (msg) => {
    const githubTimeStr = msg.published_at;
    const utcDate = new Date(githubTimeStr);
    const date = new Date(utcDate.getTime());
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    const seconds = String(date.getSeconds()).padStart(2, '0');

    const formattedDate = `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`;

    notify.info({
      avatar: () =>
        h(NAvatar, {
          size: 'small',
          round: false,
          src: icon.value,
        }),
      title: '发现新版本: ' + msg.tag_name,
      content: () => {
        return h(
          'div',
          {
            style: {
              'text-align': 'left',
              'font-size': '14px',
            },
          },
          { default: () => msg.commit?.message },
        );
      },
      duration: 5000,
      meta: '发布时间:' + formattedDate,
      action: () => {
        return h(NButton, {
          type: 'primary',
          size: 'small',
          onClick: () => {
            Environment().then(env => {
              switch (env.platform) {
                case 'windows':
                  window.open(msg.html_url);
                  break;
                default:
                  OpenURL(msg.html_url);
                  break;
              }
            });
          },
        }, { default: () => '查看' });
      },
    });
  });
});

onBeforeUnmount(() => {
  notify.destroyAll();
  EventsOff('updateVersion');
});

const handleUpdateAction = () => {
  if (!selfUpdateEnabled.value) {
    notify.info({
      title: '手动更新',
      content: manualUpdateHint.value || '请先停止程序，再用新 zip 覆盖程序目录后重新启动。',
      duration: 6000,
    });
    return;
  }
  CheckUpdate(1);
};

</script>

<template>
  <n-space vertical size="large">
    <n-card size="large">
      <n-divider title-placement="center">关于软件</n-divider>
      <n-space vertical>
        <n-image width="100" :src="icon" />
        <h1>
          <n-badge :value="versionInfo || 'dev'" :offset="[80, 10]" type="success">
            <n-gradient-text type="info" :size="50">go-stock</n-gradient-text>
          </n-badge>
        </h1>
        <n-button size="tiny" type="info" tertiary @click="handleUpdateAction">
          {{ selfUpdateEnabled ? '检查更新' : '手动更新' }}
        </n-button>
        <p v-if="!selfUpdateEnabled && manualUpdateHint" style="color: #666; margin: 0;">
          {{ manualUpdateHint }}
        </p>
        <div style="justify-self: center; text-align: left;">
          <p>go-stock 是一个本地优先的股票分析工作台，基于 Go、Vue 3、SQLite 和 Naive UI 构建，通过本机 Web 服务运行。</p>
          <p>当前公开版聚焦真正可维护的核心能力：自选股、市场资讯、AI 分析报告、推荐收益跟踪、邮件报告与运行时任务管理。</p>
          <p>当前 1.7.0 版本以“分级 AI 分析 → 最多十只股票直接模拟买入 → 下一交易日 09:50 起并行复查 → AI 判断持有或卖出 → TWR 策略评估”为研究主链路，并保留持仓期专业交易图表和多分钟数据源自动回退。</p>
          <p>来源说明：当前仓库基于 <a href="https://github.com/ArvinLovegood/go-stock" target="_blank">ArvinLovegood/go-stock</a> 改编整理而来，不是原作者官方仓库；当前维护的是公开清理版与后续改动。</p>
          <p>公开仓库已经移除个人赞赏码、联系方式、赞助码入口、私有接入说明和本地工作区配置，只保留适合协作与二次开发的公开内容。</p>
          <p>
            当前仓库：<a href="https://github.com/yxforever666gh/go-stock" target="_blank">yxforever666gh/go-stock</a>
          </p>
          <p>
            <a href="https://github.com/yxforever666gh/go-stock" target="_blank">GitHub</a><n-divider vertical />
            <a href="https://github.com/yxforever666gh/go-stock/issues" target="_blank">Issues</a><n-divider vertical />
            <a href="https://github.com/yxforever666gh/go-stock/releases" target="_blank">Releases</a>
          </p>
          <p v-if="updateLog">更新说明：{{ updateLog }}</p>
          <p v-if="officialStatement">{{ officialStatement }}</p>
          <p>
            <i style="color: crimson">本软件仅供学习研究，AI 分析结果仅供参考，不构成任何投资建议或决策依据。</i>
          </p>
        </div>
      </n-space>
      <n-divider title-placement="center">当前能力</n-divider>
      <div style="justify-self: center; text-align: left;">
        <p>支持股票自选、市场行情、研究中心、AI 分析报告、推荐收益跟踪、邮件报告和运行时任务管理。</p>
        <p>支持 OpenAI 兼容接口、DeepSeek、Ollama、LM Studio、火山方舟等模型接入。</p>
        <p>通过仅监听本机的 Web 服务提供统一界面和 API，并可通过公开仓库继续二次开发。</p>
      </div>
      <n-divider title-placement="center">公开说明</n-divider>
      <div style="justify-self: center; text-align: left;">
        <p>当前活动库已将旧策略历史永久归档；市场行情、股票、基金、普通诊股和研究中心继续保持完整。</p>
        <p>如果你准备继续二次开发，建议优先阅读 README、CHANGELOG、Release Notes 和仓库中的公开发布检查清单。</p>
      </div>
      <n-divider title-placement="center">鸣谢</n-divider>
      <div style="justify-self: center; text-align: left;">
        <p>
          感谢原始公开项目与历史贡献者提供基础能力和长期反馈：
          <a href="https://github.com/ArvinLovegood/go-stock" target="_blank">ArvinLovegood/go-stock</a>
        </p>
        <p>
          感谢以下开源项目：
          <a href="https://github.com/golang/go" target="_blank">Go</a><n-divider vertical />
          <a href="https://github.com/vuejs" target="_blank">Vue</a><n-divider vertical />
          <a href="https://github.com/tusen-ai/naive-ui" target="_blank">Naive UI</a>
        </p>
      </div>
    </n-card>
  </n-space>
</template>

<style scoped>
h1, h2 {
  margin: 0;
  padding: 6px 0;
}

p {
  margin: 2px 0;
}

ul {
  list-style-type: disc;
  padding-left: 20px;
}

a {
  color: #18a058;
  text-decoration: none;
}

a:hover {
  text-decoration: underline;
}
</style>
