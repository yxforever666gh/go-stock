<script setup>
import {h} from "vue";
import {NTag} from "naive-ui";

defineProps({
  formValue: {
    type: Object,
    required: true,
  },
  yieldEmailTestSending: {
    type: Boolean,
    required: true,
  },
  yieldEmailXlsxSending: {
    type: Boolean,
    required: true,
  },
  emailSendLogsLoading: {
    type: Boolean,
    required: true,
  },
  emailSendLogs: {
    type: Array,
    required: true,
  },
  emailSendLogPage: {
    type: Number,
    required: true,
  },
  emailSendLogTotalPages: {
    type: Number,
    required: true,
  },
  emailSendLogTotal: {
    type: Number,
    required: true,
  },
})

const emit = defineEmits([
  'immediate-change',
  'text-blur',
  'send-test',
  'send-xlsx',
  'refresh-logs',
  'prev-page',
  'next-page',
])

function formatDateTime(value) {
  if (!value) {
    return "-"
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return String(value)
  }
  const pad = (n) => String(n).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function formatSendType(value) {
  switch (String(value || "").trim()) {
    case "test":
      return "测试邮件"
    case "xlsx":
      return "收益率 XLSX"
    case "csv":
      return "收益率 CSV（旧版）"
    case "manual_ai":
      return "手动 AI 报告"
    case "cron_ai":
      return "定时 AI 报告"
    case "manual_summary":
      return "手动 AI 总结"
    case "cron_summary":
      return "定时 AI 总结"
    default:
      return value || "-"
  }
}

function formatAttachmentText(item) {
  const count = Number(item?.attachmentCount || 0)
  if (count <= 0) {
    return "-"
  }
  const names = String(item?.attachmentNames || "").trim()
  if (!names) {
    return `${count} 个附件`
  }
  return `${names} (${count} 个)`
}

function formatReportText(item) {
  const name = String(item?.reportStockName || "").trim()
  const code = String(item?.reportStockCode || "").trim()
  if (name && code) {
    return `${name} [${code}]`
  }
  if (name || code) {
    return name || code
  }
  return "-"
}

const emailSendLogColumns = [
  { title: '触发时间', key: 'triggeredAt', width: 168, render: (row) => formatDateTime(row.triggeredAt || row.CreatedAt) },
  { title: '类型', key: 'sendType', width: 120, render: (row) => formatSendType(row.sendType) },
  { title: '状态', key: 'status', width: 90, render: (row) => row.status === 'success' ? h(NTag, { type: 'success', bordered: false }, () => '成功') : h(NTag, { type: 'error', bordered: false }, () => '失败') },
  { title: '收件人', key: 'recipients', width: 220, ellipsis: { tooltip: true } },
  { title: '主题', key: 'subject', width: 260, ellipsis: { tooltip: true } },
  { title: '报告', key: 'report', width: 180, render: (row) => formatReportText(row) },
  { title: '附件', key: 'attachmentNames', width: 220, render: (row) => formatAttachmentText(row), ellipsis: { tooltip: true } },
  { title: '摘要', key: 'extraSummary', width: 220, ellipsis: { tooltip: true } },
  { title: '错误信息', key: 'errorMessage', minWidth: 260, ellipsis: { tooltip: true }, render: (row) => row.errorMessage || '-' }
]
</script>

<template>
  <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => '通知设置')" size="small">
    <n-grid :cols="24" :x-gap="24" style="text-align: left">
      <n-form-item-gi :span="4" label="邮箱推送收益率：" path="yieldEmail.enable">
        <n-switch v-model:value="formValue.yieldEmail.enable" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>

      <n-form-item-gi :span="24" v-if="formValue.yieldEmail.enable">
        <n-alert type="info" :show-icon="false">
          支持多个收件邮箱。邮件不会再单独按时间定时发送；现在改为市场资讯 AI 总结在“定时执行完成后”立即发邮件。手动点击“再次总结”不会自动发，如需手动发，请到 AI 总结弹窗里点“发送邮件”。
        </n-alert>
      </n-form-item-gi>
      <n-form-item-gi :span="12" v-if="formValue.yieldEmail.enable" label="收件邮箱：" path="yieldEmail.to">
        <n-input placeholder="多个收件邮箱用英文逗号分隔" v-model:value="formValue.yieldEmail.to" clearable @blur="emit('text-blur')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="12" v-if="formValue.yieldEmail.enable" label="发件邮箱：" path="yieldEmail.from">
        <n-input placeholder="用于 SMTP 登录的发件邮箱" v-model:value="formValue.yieldEmail.from" clearable @blur="emit('text-blur')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="8" v-if="formValue.yieldEmail.enable" label="SMTP 主机：" path="yieldEmail.smtpHost">
        <n-input placeholder="可留空，按发件邮箱自动推断" v-model:value="formValue.yieldEmail.smtpHost" clearable @blur="emit('text-blur')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="4" v-if="formValue.yieldEmail.enable" label="SMTP 端口：" path="yieldEmail.smtpPort">
        <n-input-number v-model:value="formValue.yieldEmail.smtpPort" :min="1" :max="65535" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="6" v-if="formValue.yieldEmail.enable" label="SMTP 用户名：" path="yieldEmail.smtpUsername">
        <n-input placeholder="可留空，默认使用发件邮箱" v-model:value="formValue.yieldEmail.smtpUsername" clearable @blur="emit('text-blur')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="6" v-if="formValue.yieldEmail.enable" label="SMTP 授权码：" path="yieldEmail.smtpPassword">
        <n-input type="password" placeholder="邮箱 SMTP 授权码/密码" v-model:value="formValue.yieldEmail.smtpPassword" show-password-on="click" clearable @blur="emit('text-blur')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="8" v-if="formValue.yieldEmail.enable" label="定时AI总结后自动发邮件：" path="marketSummaryEmailEnabled">
        <n-switch v-model:value="formValue.marketSummaryEmailEnabled" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="24" v-if="formValue.yieldEmail.enable">
        <n-space vertical>
          <n-space>
            <n-button type="primary" :loading="yieldEmailTestSending" @click="emit('send-test')">发送“你好”测试邮件</n-button>
            <n-button type="success" :loading="yieldEmailXlsxSending" @click="emit('send-xlsx')">立刻发送收益率 XLSX</n-button>
            <n-button tertiary @click="emit('refresh-logs')" :loading="emailSendLogsLoading">刷新发送日志</n-button>
          </n-space>
          <n-text depth="3">收益率 XLSX 会单独发送与网页收益率列表一致的全量表格，不受页面 100 条分页限制，并保留主要状态颜色。若开启上面的开关，只有“市场资讯 AI 总结定时任务”完成后才会自动发邮件；手动总结不会自动发。</n-text>
        </n-space>
      </n-form-item-gi>
      <n-form-item-gi :span="24" v-if="formValue.yieldEmail.enable">
        <n-card size="small" title="最近邮件发送日志">
          <n-data-table
              :loading="emailSendLogsLoading"
              :bordered="false"
              :single-line="false"
              size="small"
              :columns="emailSendLogColumns"
              :data="emailSendLogs"
              :pagination="false"
          />
          <n-flex justify="space-between" align="center" style="margin-top: 12px;">
            <n-text depth="3">
              第 {{ emailSendLogPage }} / {{ emailSendLogTotalPages }} 页，共 {{ emailSendLogTotal }} 条
            </n-text>
            <n-space>
              <n-button size="small" @click="emit('prev-page')" :disabled="emailSendLogPage <= 1 || emailSendLogsLoading">
                上一页
              </n-button>
              <n-button size="small" @click="emit('next-page')" :disabled="emailSendLogPage >= emailSendLogTotalPages || emailSendLogsLoading">
                下一页
              </n-button>
            </n-space>
          </n-flex>
        </n-card>
      </n-form-item-gi>
    </n-grid>
  </n-card>
</template>
