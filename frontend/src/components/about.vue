<script setup>
import 'md-editor-v3/lib/preview.css';
import { h, onBeforeUnmount, onMounted, ref } from 'vue';
import { CheckUpdate, GetVersionInfo, GetSponsorInfo, OpenURL } from '../services/app-api';
import { Environment, EventsOff, EventsOn } from '../../wailsjs/runtime';
import { NAvatar, NButton, useNotification } from 'naive-ui';
import { format } from 'date-fns';

const updateLog = ref('');
const versionInfo = ref('');
const icon = ref('');
const officialStatement = ref('');
const notify = useNotification();
const vipLevel = ref('');
const vipEndTime = ref('');
const expired = ref(false);
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

    GetSponsorInfo().then((res) => {
      vipLevel.value = res.vipLevel;
      vipEndTime.value = res.vipEndTime;
      if (res.vipLevel) {
        if (res.vipEndTime < format(new Date(), 'yyyy-MM-dd HH:mm:ss')) {
          notify.warning({ content: 'VIP已到期' });
          expired.value = true;
        }
      }
    });
  });
});

onBeforeUnmount(() => {
  notify.destroyAll();
  EventsOff('updateVersion');
});

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
  <n-space vertical size="large" style="--wails-draggable:no-drag">
    <n-card size="large">
      <n-divider title-placement="center">关于软件</n-divider>
      <n-space vertical>
        <n-image width="100" :src="icon" />
        <h1>
          <n-badge v-if="!vipLevel" :value="versionInfo || 'dev'" :offset="[80, 10]" type="success">
            <n-gradient-text type="info" :size="50">go-stock</n-gradient-text>
          </n-badge>
          <n-badge v-else :value="versionInfo || 'dev'" :offset="[70, 10]" type="success">
            <n-gradient-text :type="expired ? 'error' : 'warning'" :size="50">go-stock</n-gradient-text>
            <n-tag :bordered="false" size="small" type="warning">VIP{{ vipLevel }}</n-tag>
          </n-badge>
        </h1>
        <n-gradient-text v-if="vipLevel" :type="expired ? 'error' : 'warning'">
          vip到期时间：{{ vipEndTime }}
        </n-gradient-text>
        <n-button size="tiny" type="info" tertiary @click="handleUpdateAction">
          {{ selfUpdateEnabled ? '检查更新' : '手动更新' }}
        </n-button>
        <p v-if="!selfUpdateEnabled && manualUpdateHint" style="color: #666; margin: 0;">
          {{ manualUpdateHint }}
        </p>
        <div style="justify-self: center; text-align: left;">
          <p>go-stock 是基于 Go、Wails、Vue 3 和 Naive UI 构建的股票分析工具，支持桌面模式和本地 Web 模式。</p>
          <p>当前公开版已移除个人赞赏码、联系方式、私有接入说明和本地工作区配置，只保留适合公开仓库的核心功能与版本信息。</p>
          <p>
            当前仓库：
            <a href="https://github.com/yxforever666gh/go-stock" target="_blank">yxforever666gh/go-stock</a>
            <n-divider vertical />
            原始公开项目：
            <a href="https://github.com/ArvinLovegood/go-stock" target="_blank">ArvinLovegood/go-stock</a>
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
        <p>支持桌面模式与 <code>--web</code> 模式，共用核心业务逻辑。</p>
      </div>
      <n-divider title-placement="center">公开说明</n-divider>
      <div style="justify-self: center; text-align: left;">
        <p>当前 `1.2.4` 公开快照已经清理构建产物、运行数据库、支付二维码和本地私有说明，适合作为对外公开仓库的基线版本。</p>
        <p>如果你准备继续二次开发，建议优先阅读 README、CHANGELOG 和仓库中的公开发布检查清单。</p>
      </div>
      <n-divider title-placement="center">鸣谢</n-divider>
      <div style="justify-self: center; text-align: left;">
        <p>
          感谢原始公开项目与历史贡献者提供基础能力和长期反馈：
          <a href="https://github.com/ArvinLovegood/go-stock" target="_blank">ArvinLovegood/go-stock</a>
        </p>
        <p>
          感谢以下开源项目：
          <a href="https://github.com/wailsapp/wails" target="_blank">Wails</a><n-divider vertical />
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
