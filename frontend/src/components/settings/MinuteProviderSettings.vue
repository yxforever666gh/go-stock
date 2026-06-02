<script setup>
import {h} from "vue";
import {NTag} from "naive-ui";

defineProps({
  formValue: {
    type: Object,
    required: true,
  },
  minuteProviderModeOptions: {
    type: Array,
    required: true,
  },
  akshareMinuteSourceOptions: {
    type: Array,
    required: true,
  },
  privateMinuteProxyModeOptions: {
    type: Array,
    required: true,
  },
  privateMinuteLevelOptions: {
    type: Array,
    required: true,
  },
})

const emit = defineEmits(['immediate-change', 'text-blur'])
</script>

<template>
  <n-card :title="() => h(NTag, { type: 'primary', bordered: false }, () => '分钟线数据源')" size="small">
    <n-grid :cols="24" :x-gap="24" style="text-align: left">
      <n-form-item-gi :span="24">
        <n-alert type="info" :show-icon="false">
          公共源更适合实时与短周期分钟线。若要覆盖更长时间的历史分钟线，请切换到私人分钟线来源并填写调用 URL 与 API Key。
        </n-alert>
      </n-form-item-gi>

      <n-form-item-gi :span="8" label="分钟线模式：" path="minuteProviderMode">
        <n-radio-group v-model:value="formValue.minuteProviderMode" @update:value="emit('immediate-change')">
          <n-space>
            <n-radio-button
                v-for="item in minuteProviderModeOptions"
                :key="item.value"
                :value="item.value"
            >
              {{ item.label }}
            </n-radio-button>
          </n-space>
        </n-radio-group>
      </n-form-item-gi>
      <n-form-item-gi :span="8" label="AKShare 来源偏好：" path="akshareMinuteSourceMode">
        <n-select
            v-model:value="formValue.akshareMinuteSourceMode"
            :options="akshareMinuteSourceOptions"
            :disabled="!formValue.akshareEnabled"
            @update:value="emit('immediate-change')"
        />
      </n-form-item-gi>

      <n-form-item-gi :span="4" label="AKShare：" path="akshareEnabled">
        <n-switch v-model:value="formValue.akshareEnabled" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="4" label="新浪分钟线：" path="sinaMinuteEnabled">
        <n-switch v-model:value="formValue.sinaMinuteEnabled" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="4" label="腾讯分钟线：" path="tencentMinuteEnabled">
        <n-switch v-model:value="formValue.tencentMinuteEnabled" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="24">
        <n-alert type="warning" :show-icon="false">
          私人分钟线来源不会在页面中展示具体服务商名称；这里只提供通用 URL 与 API Key 配置入口。
        </n-alert>
      </n-form-item-gi>

      <n-form-item-gi :span="4" label="启用私人来源：" path="privateMinute.enabled">
        <n-switch v-model:value="formValue.privateMinute.enabled" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="10" label="调用 URL：" path="privateMinute.baseUrl">
        <n-input
            type="text"
            placeholder="例如 https://example.com/api"
            v-model:value="formValue.privateMinute.baseUrl"
            clearable
            @blur="emit('text-blur')"
        />
      </n-form-item-gi>
      <n-form-item-gi :span="10" label="API Key：" path="privateMinute.apiKey">
        <n-input
            type="password"
            placeholder="私人分钟线来源 API Key"
            v-model:value="formValue.privateMinute.apiKey"
            show-password-on="click"
            clearable
            @blur="emit('text-blur')"
        />
      </n-form-item-gi>
      <n-form-item-gi :span="6" label="超时(秒)：" path="privateMinute.timeoutSec">
        <n-input-number :min="1" v-model:value="formValue.privateMinute.timeoutSec" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="6" label="最小间隔(ms)：" path="privateMinute.minIntervalMs">
        <n-input-number :min="0" v-model:value="formValue.privateMinute.minIntervalMs" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="6" label="代理模式：" path="privateMinute.proxyMode">
        <n-select v-model:value="formValue.privateMinute.proxyMode" :options="privateMinuteProxyModeOptions" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
      <n-form-item-gi :span="6" label="分钟级别：" path="privateMinute.level">
        <n-select v-model:value="formValue.privateMinute.level" :options="privateMinuteLevelOptions" @update:value="emit('immediate-change')"/>
      </n-form-item-gi>
    </n-grid>
  </n-card>
</template>
