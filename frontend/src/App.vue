<script setup>
import 'md-editor-v3/lib/style.css'
import {computed, onBeforeUnmount, onMounted, ref, watch} from 'vue'
import {useRoute} from 'vue-router'
import {darkTheme, dateZhCN, zhCN} from 'naive-ui'
import {GetConfig} from './services/settings-api'
import {GetVersionInfo, Shutdown} from './services/system-api'
import {EventsOn, WindowSetTitle} from './services/browser-runtime.mjs'
import {createMenuOptions} from './app-shell/menu-options'

const route = useRoute()
const activeKey = computed(() => String(route.name || 'prediction'))
const menuOptions = createMenuOptions()
watch(activeKey, key => WindowSetTitle(`Stock God · ${{prediction: '股票预测', settings: '设置', about: '关于'}[key] || '股票预测'}`), {immediate: true})
const theme = ref(null)
const version = ref('')
const shuttingDown = ref(false)
const shutdownMessage = ref('')
async function loadTheme() {
  try { theme.value = (await GetConfig()).darkTheme ? darkTheme : null }
  catch (error) { console.warn('[Stock God] 无法读取主题', error) }
}
const stopSettingsListener = EventsOn('updateSettings', loadTheme)
onBeforeUnmount(stopSettingsListener)
onMounted(async () => {
  await loadTheme()
  try { version.value = (await GetVersionInfo()).version || '' } catch (error) { console.warn('[Stock God] 无法读取版本', error) }
})
async function requestShutdown() {
  if (!window.confirm('确定要停止 Stock God 本地服务并退出吗？')) return
  shuttingDown.value = true
  try {
    await Shutdown()
    shutdownMessage.value = 'Stock God 已退出，可以关闭此页面'
  } catch (error) {
    shutdownMessage.value = `退出失败：${error?.message || error}`
    shuttingDown.value = false
  }
}
</script>

<template>
  <n-config-provider :theme="theme" :locale="zhCN" :date-locale="dateZhCN">
    <n-global-style/>
    <n-message-provider>
      <n-notification-provider>
        <n-modal-provider>
          <n-dialog-provider>
            <header class="app-header">
              <strong>Stock God <small v-if="version">{{ version }}</small></strong>
              <n-menu :value="activeKey" mode="horizontal" :options="menuOptions"/>
              <n-button tertiary type="error" size="small" :loading="shuttingDown" @click="requestShutdown">退出</n-button>
            </header>
            <main class="app-content">
              <n-alert v-if="shutdownMessage" :type="shuttingDown ? 'success' : 'error'" class="shutdown-message">{{ shutdownMessage }}</n-alert>
              <RouterView/>
            </main>
          </n-dialog-provider>
        </n-modal-provider>
      </n-notification-provider>
    </n-message-provider>
  </n-config-provider>
</template>

<style scoped>
.app-header {display: flex; align-items: center; gap: 16px; padding: 4px 20px; border-bottom: 1px solid #8883; flex-wrap: wrap;}
.app-header strong {font-size: 20px; white-space: nowrap;}
.app-header small {font-size: 12px; font-weight: normal; opacity: .7;}
.app-header .n-menu {flex: 1; min-width: 250px;}
.app-content {padding: 16px;}
.shutdown-message {margin-bottom: 12px;}
</style>
