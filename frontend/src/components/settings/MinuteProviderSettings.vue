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
          这里设置的是回退优先级，不是互斥模式。当前来源报错、返回空数据或覆盖不完整时，会按顺序自动尝试下一个已启用来源；已取得的数据会保留。
        </n-alert>
      </n-form-item-gi>

      <n-form-item-gi :span="8" label="来源优先级：" path="minuteProviderMode">
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
      <n-form-item-gi :span="24">
        <n-alert type="success" :show-icon="false">
          {{ formValue.minuteProviderMode === 'private'
            ? '回退顺序：私人来源 → 腾讯 → 新浪 → AKShare（新浪/东方财富）'
            : '回退顺序：腾讯 → 新浪 → AKShare（新浪/东方财富）→ 私人来源' }}。关闭的数据源不会被调用。
        </n-alert>
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
          私人来源仅在启用、配置完整且分钟级别为 1 分钟时参与研究图表回退；公共源更适合实时与短周期，私人源可用于补充较长历史。
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
