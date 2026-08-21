<script setup>
import {onBeforeUnmount, ref} from 'vue'

const props = defineProps({
  formValue: {type: Object, required: true},
  akshareMinuteSourceOptions: {type: Array, required: true},
  privateMinuteProxyModeOptions: {type: Array, required: true},
  privateMinuteLevelOptions: {type: Array, required: true},
})

const emit = defineEmits(['immediate-change', 'text-blur', 'move-provider'])
const dragSourceIndex = ref(null)
const dragTargetIndex = ref(null)

const providerMeta = {
  tencent: {name: '腾讯分钟线', description: '公共实时分钟线'},
  sina: {name: '新浪分钟线', description: '公共实时分钟线'},
  akshare: {name: 'AKShare', description: '新浪 / 东方财富适配'},
  private: {name: '私人分钟线接口', description: '自定义 HTTP 数据接口'},
}

function providerEnabled(provider) {
  if (provider === 'tencent') return props.formValue.tencentMinuteEnabled
  if (provider === 'sina') return props.formValue.sinaMinuteEnabled
  if (provider === 'akshare') return props.formValue.akshareEnabled
  return props.formValue.privateMinute.enabled
}

function setProviderEnabled(provider, value) {
  if (provider === 'tencent') props.formValue.tencentMinuteEnabled = value
  else if (provider === 'sina') props.formValue.sinaMinuteEnabled = value
  else if (provider === 'akshare') props.formValue.akshareEnabled = value
  else props.formValue.privateMinute.enabled = value
  emit('immediate-change')
}

function startDrag(event, index) {
  dragSourceIndex.value = index
  dragTargetIndex.value = index
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', String(index))
}

function dragOver(event, index) {
  event.preventDefault()
  dragTargetIndex.value = index
  event.dataTransfer.dropEffect = 'move'
}

function move(sourceIndex, targetIndex) {
  if (!Number.isInteger(sourceIndex) || !Number.isInteger(targetIndex) || sourceIndex === targetIndex) return
  emit('move-provider', sourceIndex, targetIndex)
}

function drop(event, index) {
  event.preventDefault()
  const source = dragSourceIndex.value ?? Number(event.dataTransfer.getData('text/plain'))
  move(Number(source), index)
  finishDrag()
}

function finishDrag() {
  dragSourceIndex.value = null
  dragTargetIndex.value = null
  cleanupPointerDrag()
}

function rowIndexAt(clientX, clientY) {
  const row = document.elementFromPoint(clientX, clientY)?.closest?.('.provider-row')
  const index = Number(row?.getAttribute?.('data-provider-index'))
  return Number.isInteger(index) ? index : null
}

function pointerDown(event, index) {
  if (event.button !== 0) return
  dragSourceIndex.value = index
  dragTargetIndex.value = index
  cleanupPointerDrag()
  window.addEventListener('pointermove', pointerMove)
  window.addEventListener('pointerup', pointerUp, {once: true})
  window.addEventListener('pointercancel', finishDrag, {once: true})
  event.preventDefault()
}

function pointerMove(event) {
  if (dragSourceIndex.value === null) return
  const index = rowIndexAt(event.clientX, event.clientY)
  if (index !== null) dragTargetIndex.value = index
}

function pointerUp(event) {
  const target = rowIndexAt(event.clientX, event.clientY) ?? dragTargetIndex.value
  move(Number(dragSourceIndex.value), Number(target))
  finishDrag()
}

function cleanupPointerDrag() {
  window.removeEventListener('pointermove', pointerMove)
  window.removeEventListener('pointerup', pointerUp)
  window.removeEventListener('pointercancel', finishDrag)
}

function rowClass(index) {
  return {
    'provider-row': true,
    'provider-row-dragging': dragSourceIndex.value === index,
    'provider-row-drag-over': dragTargetIndex.value === index && dragSourceIndex.value !== index,
  }
}

onBeforeUnmount(cleanupPointerDrag)
</script>

<template>
  <n-space vertical>
    <n-alert type="info" :show-icon="false">
      从上到下依次调用；当前接口报错、无数据或覆盖不完整时，会继续尝试下一个已启用接口。
    </n-alert>
    <n-scrollbar x-scrollable>
      <n-table size="small" :bordered="true" :single-line="false" style="min-width: 1600px">
        <thead>
        <tr>
          <th style="width: 90px">顺序</th>
          <th style="width: 80px">启用</th>
          <th style="width: 180px">数据接口</th>
          <th style="width: 200px">来源选项</th>
          <th style="width: 310px">调用 URL</th>
          <th style="width: 250px">API Key</th>
          <th style="width: 120px">超时(秒)</th>
          <th style="width: 140px">最小间隔(ms)</th>
          <th style="width: 180px">代理模式</th>
          <th style="width: 140px">分钟级别</th>
        </tr>
        </thead>
        <tbody>
        <tr v-for="(provider, index) in formValue.minuteProviderOrder"
            :key="provider"
            :class="rowClass(index)"
            :data-provider-index="index"
            @dragover="dragOver($event, index)"
            @drop="drop($event, index)"
            @dragend="finishDrag">
          <td>
            <div class="provider-drag-handle" draggable="true" title="拖动调整调用顺序"
                 @pointerdown="pointerDown($event, index)" @dragstart="startDrag($event, index)">
              <span class="provider-drag-icon">≡</span><span>{{ index + 1 }}</span>
            </div>
          </td>
          <td>
            <n-switch :value="providerEnabled(provider)"
                      :aria-label="`${providerMeta[provider].name}启用开关`"
                      @update:value="value => setProviderEnabled(provider, value)"/>
          </td>
          <td>
            <n-text strong>{{ providerMeta[provider].name }}</n-text><br>
            <n-text depth="3">{{ providerMeta[provider].description }}</n-text>
          </td>
          <td>
            <n-select v-if="provider === 'akshare'" v-model:value="formValue.akshareMinuteSourceMode"
                      :options="akshareMinuteSourceOptions" @update:value="emit('immediate-change')"/>
            <n-text v-else depth="3">—</n-text>
          </td>
          <td>
            <n-input v-if="provider === 'private'" v-model:value="formValue.privateMinute.baseUrl"
                     placeholder="https://example.com/api" clearable @blur="emit('text-blur')"/>
            <n-text v-else depth="3">内置</n-text>
          </td>
          <td>
            <n-input v-if="provider === 'private'" v-model:value="formValue.privateMinute.apiKey"
                     type="password" placeholder="API Key" show-password-on="click" clearable
                     @blur="emit('text-blur')"/>
            <n-text v-else depth="3">—</n-text>
          </td>
          <td>
            <n-input-number v-if="provider === 'private'" v-model:value="formValue.privateMinute.timeoutSec"
                            :min="1" @update:value="emit('immediate-change')"/>
            <n-text v-else depth="3">—</n-text>
          </td>
          <td>
            <n-input-number v-if="provider === 'private'" v-model:value="formValue.privateMinute.minIntervalMs"
                            :min="0" @update:value="emit('immediate-change')"/>
            <n-text v-else depth="3">—</n-text>
          </td>
          <td>
            <n-select v-if="provider === 'private'" v-model:value="formValue.privateMinute.proxyMode"
                      :options="privateMinuteProxyModeOptions" @update:value="emit('immediate-change')"/>
            <n-text v-else depth="3">—</n-text>
          </td>
          <td>
            <n-select v-if="provider === 'private'" v-model:value="formValue.privateMinute.level"
                      :options="privateMinuteLevelOptions" @update:value="emit('immediate-change')"/>
            <n-text v-else depth="3">1 分钟</n-text>
          </td>
        </tr>
        </tbody>
      </n-table>
    </n-scrollbar>
  </n-space>
</template>

<style scoped>
.provider-row { transition: background-color .12s ease, opacity .12s ease; }
.provider-row-dragging { opacity: .58; }
.provider-row-drag-over > td { background-color: rgba(24, 160, 88, .08); border-top: 2px dashed #18a058; }
.provider-drag-handle { align-items: center; color: #18a058; cursor: move; display: inline-flex; font-weight: 600; gap: 8px; justify-content: center; min-width: 54px; user-select: none; }
.provider-drag-icon { color: #8a8f99; font-size: 18px; line-height: 1; }
</style>
