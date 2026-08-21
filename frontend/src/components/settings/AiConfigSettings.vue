<script setup>
import {onBeforeUnmount, ref} from "vue";

defineProps({
  formValue: {
    type: Object,
    required: true,
  },
  aiProtocolOptions: {
    type: Array,
    required: true,
  },
  aiConfigRowKey: {
    type: Function,
    required: true,
  },
  aiConfigTestState: {
    type: Function,
    required: true,
  },
})

const emit = defineEmits([
  'immediate-change',
  'text-blur',
  'add-ai-config',
  'remove-ai-config',
  'test-ai-config',
  'move-ai-config',
])

const aiConfigDragSourceIndex = ref(null)
const aiConfigDragTargetIndex = ref(null)

function handleAiConfigDragStart(event, index) {
  aiConfigDragSourceIndex.value = index
  aiConfigDragTargetIndex.value = index
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', String(index))
}

function handleAiConfigDragOver(event, index) {
  event.preventDefault()
  aiConfigDragTargetIndex.value = index
  event.dataTransfer.dropEffect = 'move'
}

function handleAiConfigDragEnter(event, index) {
  event.preventDefault()
  aiConfigDragTargetIndex.value = index
}

function moveAiConfig(sourceIndex, targetIndex) {
  if (
      sourceIndex === null ||
      targetIndex === null ||
      sourceIndex === targetIndex ||
      !Number.isInteger(sourceIndex) ||
      !Number.isInteger(targetIndex)
  ) {
    return
  }
  emit('move-ai-config', sourceIndex, targetIndex)
}

function handleAiConfigDrop(event, index) {
  event.preventDefault()
  const rawSource = aiConfigDragSourceIndex.value ?? Number(event.dataTransfer.getData('text/plain'))
  moveAiConfig(Number(rawSource), index)
  aiConfigDragSourceIndex.value = null
  aiConfigDragTargetIndex.value = null
}

function handleAiConfigDragEnd() {
  aiConfigDragSourceIndex.value = null
  aiConfigDragTargetIndex.value = null
}

function resolveAiConfigRowIndexFromPoint(clientX, clientY) {
  const target = document.elementFromPoint(clientX, clientY)
  const row = target?.closest?.('.ai-config-row')
  const rawIndex = row?.getAttribute?.('data-ai-config-index')
  const index = Number(rawIndex)
  return Number.isInteger(index) ? index : null
}

function cleanupAiConfigPointerDrag() {
  window.removeEventListener('pointermove', handleAiConfigPointerMove)
  window.removeEventListener('pointerup', handleAiConfigPointerUp)
  window.removeEventListener('pointercancel', handleAiConfigPointerCancel)
}

function handleAiConfigPointerDown(event, index) {
  if (event.button !== 0) {
    return
  }
  aiConfigDragSourceIndex.value = index
  aiConfigDragTargetIndex.value = index
  cleanupAiConfigPointerDrag()
  window.addEventListener('pointermove', handleAiConfigPointerMove)
  window.addEventListener('pointerup', handleAiConfigPointerUp, {once: true})
  window.addEventListener('pointercancel', handleAiConfigPointerCancel, {once: true})
  event.preventDefault()
}

function handleAiConfigPointerMove(event) {
  if (aiConfigDragSourceIndex.value === null) {
    return
  }
  const index = resolveAiConfigRowIndexFromPoint(event.clientX, event.clientY)
  if (index !== null) {
    aiConfigDragTargetIndex.value = index
  }
}

function handleAiConfigPointerUp(event) {
  const fallbackTarget = aiConfigDragTargetIndex.value
  const pointTarget = resolveAiConfigRowIndexFromPoint(event.clientX, event.clientY)
  const targetIndex = pointTarget ?? fallbackTarget
  moveAiConfig(Number(aiConfigDragSourceIndex.value), Number(targetIndex))
  aiConfigDragSourceIndex.value = null
  aiConfigDragTargetIndex.value = null
  cleanupAiConfigPointerDrag()
}

function handleAiConfigPointerCancel() {
  aiConfigDragSourceIndex.value = null
  aiConfigDragTargetIndex.value = null
  cleanupAiConfigPointerDrag()
}

function aiConfigRowClass(index) {
  return {
    'ai-config-row': true,
    'ai-config-row-dragging': aiConfigDragSourceIndex.value === index,
    'ai-config-row-drag-over': aiConfigDragTargetIndex.value === index && aiConfigDragSourceIndex.value !== index,
  }
}

onBeforeUnmount(() => {
  cleanupAiConfigPointerDrag()
})
</script>

<template>
  <n-space vertical>
    <n-divider title-placement="left">模型调用顺序</n-divider>
    <n-text depth="3">从上到下依次调用；当前模型失败时回退到下一个已启用模型。关闭的模型不会被自动调用。</n-text>
    <n-scrollbar x-scrollable>
      <n-table size="small" :bordered="true" :single-line="false" style="min-width: 1750px;">
        <thead>
        <tr>
          <th style="width: 100px;">回退顺序</th>
          <th style="width: 82px;">调用</th>
          <th style="width: 190px;">名称</th>
          <th style="width: 320px;">Base URL</th>
          <th style="width: 230px;">Model</th>
          <th style="width: 190px;">API 格式</th>
          <th style="width: 300px;">API Key</th>
          <th style="width: 110px;">测试</th>
          <th style="width: 90px;">删除</th>
        </tr>
        </thead>
        <tbody>
        <template v-for="(aiConfig, index) in formValue.openAI.aiConfigs" :key="aiConfigRowKey(aiConfig)">
          <tr :class="aiConfigRowClass(index)"
              :data-ai-config-index="index"
              @dragover="handleAiConfigDragOver($event, index)"
              @dragenter="handleAiConfigDragEnter($event, index)"
              @drop="handleAiConfigDrop($event, index)"
              @dragend="handleAiConfigDragEnd">
            <td>
              <div class="ai-config-drag-handle"
                   draggable="true"
                   title="拖动调整回退顺序"
                   @pointerdown="handleAiConfigPointerDown($event, index)"
                   @dragstart="handleAiConfigDragStart($event, index)"
                   @dragend="handleAiConfigDragEnd">
                <span class="ai-config-drag-icon">≡</span>
                <span>{{ index + 1 }}</span>
              </div>
            </td>
            <td>
              <n-switch :value="!aiConfig.disabled"
                        :aria-label="`${aiConfig.name || '未命名模型'}调用开关`"
                        @update:value="value => { aiConfig.disabled = !value; emit('immediate-change') }"/>
            </td>
            <td>
              <n-input v-model:value="aiConfig.name" type="text" placeholder="名称" clearable
                       @blur="emit('text-blur')"/>
            </td>
            <td>
              <n-input v-model:value="aiConfig.baseUrl" type="text" placeholder="https://api.example.com/v1"
                       clearable @blur="emit('text-blur')"/>
            </td>
            <td>
              <n-input v-model:value="aiConfig.modelName" type="text" placeholder="模型名称" clearable
                       @blur="emit('text-blur')"/>
            </td>
            <td>
              <n-select v-model:value="aiConfig.apiProtocol" :options="aiProtocolOptions"
                        @update:value="emit('immediate-change')"/>
            </td>
            <td>
              <n-input v-model:value="aiConfig.apiKey" type="password" placeholder="API Key" clearable
                       show-password-on="click" @blur="emit('text-blur')"/>
            </td>
            <td>
              <n-button size="small" type="primary" ghost
                        :loading="aiConfigTestState(aiConfig, index).loading"
                        @click="emit('test-ai-config', index)">测试</n-button>
            </td>
            <td>
              <n-button type="error" size="small" ghost @click="emit('remove-ai-config', index)">删除</n-button>
            </td>
          </tr>
          <tr v-if="aiConfigTestState(aiConfig, index).result">
            <td colspan="9">
              <n-alert :type="aiConfigTestState(aiConfig, index).result.success ? 'success' : 'error'"
                       :bordered="false">
                {{ aiConfigTestState(aiConfig, index).result.message }}
                <template v-if="aiConfigTestState(aiConfig, index).result.success">
                  ：{{ aiConfigTestState(aiConfig, index).result.protocol }} /
                  {{ aiConfigTestState(aiConfig, index).result.model }} /
                  {{ aiConfigTestState(aiConfig, index).result.latencyMs }}ms /
                  {{ aiConfigTestState(aiConfig, index).result.contentPreview }}
                </template>
              </n-alert>
            </td>
          </tr>
        </template>
        </tbody>
      </n-table>
    </n-scrollbar>
    <n-button type="primary" dashed @click="emit('add-ai-config')" style="width: 100%;">+ 添加AI配置</n-button>
  </n-space>
</template>

<style scoped>
.ai-config-row {
  transition: background-color 0.12s ease, opacity 0.12s ease;
}

.ai-config-row-dragging {
  opacity: 0.58;
}

.ai-config-row-drag-over > td {
  background-color: rgba(24, 160, 88, 0.08);
  border-top: 2px dashed #18a058;
}

.ai-config-drag-handle {
  align-items: center;
  color: #18a058;
  cursor: move;
  display: inline-flex;
  font-weight: 600;
  gap: 8px;
  justify-content: center;
  min-width: 54px;
  user-select: none;
}

.ai-config-drag-icon {
  color: #8a8f99;
  font-size: 18px;
  line-height: 1;
}
</style>
