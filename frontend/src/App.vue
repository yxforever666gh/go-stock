<script setup>
import 'md-editor-v3/lib/style.css'
import {
  WindowFullscreen,
  WindowUnfullscreen,
  WindowSetTitle,
} from '../wailsjs/runtime'
import { onBeforeMount, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { darkTheme, dateZhCN, zhCN } from 'naive-ui'
import { GetConfig, GetGroupList, GetVersionInfo } from './services/app-api'
import {
  applyFeatureMenuVisibility,
  createMenuOptions,
  replaceStockGroupMenuOptions,
} from './app-shell/menu-options'
import { registerAppRuntimeEvents } from './app-shell/runtime-events'

const router = useRouter()
const loading = ref(true)
const loadingMsg = ref('加载数据中...')
const contentStyle = ref('')
const enableFund = ref(false)
const enableAgent = ref(false)
const enableDarkTheme = ref(null)
const content = ref('数据来源于网络，仅供参考；投资有风险，入市需谨慎')
const isFullscreen = ref(false)
const activeKey = ref('stock')
const containerRef = ref({})
const realtimeProfit = ref(0)
const groupList = ref([])
const officialStatement = ref('')
const menuOptions = ref([])
let cleanupRuntimeEvents = () => {}

function toggleFullscreen(e) {
  activeKey.value = 'full'
  //console.log(e)
  if (isFullscreen.value) {
    WindowUnfullscreen()
    //e.target.innerHTML = '全屏'
  } else {
    WindowFullscreen()
    // e.target.innerHTML = '取消全屏'
  }
  isFullscreen.value = !isFullscreen.value
}

// const drag = ref(false)
// const lastPos= ref({x:0,y:0})
// function toggleStartMoveWindow(e) {
//   drag.value=!drag.value
//   lastPos.value={x:e.clientX,y:e.clientY}
// }
// function dragstart(e) {
//   if (drag.value) {
//     let x=e.clientX-lastPos.value.x
//     let y=e.clientY-lastPos.value.y
//     WindowGetPosition().then((pos) => {
//       WindowSetPosition(pos.x+x,pos.y+y)
//     })
//   }
// }
// window.addEventListener('mousemove', dragstart)

onBeforeUnmount(() => {
  cleanupRuntimeEvents()
})

function syncFeatureFlags(res) {
  enableFund.value = res.enableFund
  enableAgent.value = res.enableAgent
  applyFeatureMenuVisibility(menuOptions.value, {
    enableFund: res.enableFund,
    enableAgent: res.enableAgent,
  })
  enableDarkTheme.value = res.darkTheme ? darkTheme : null
}

onBeforeMount(() => {
  menuOptions.value = createMenuOptions({
    router,
    activeKey,
    enableFund,
    enableAgent,
    realtimeProfit,
    isFullscreen,
    toggleFullscreen,
  })
  cleanupRuntimeEvents = registerAppRuntimeEvents({
    loading,
    loadingMsg,
    realtimeProfit,
  })

  GetVersionInfo().then(result => {
    if (result.officialStatement) {
      content.value = result.officialStatement + '\n\n' + content.value
      officialStatement.value = result.officialStatement
    }
  })

  GetGroupList().then(result => {
    groupList.value = result
    replaceStockGroupMenuOptions(menuOptions.value, router, groupList.value)
  })

  GetConfig().then((res) => {
    syncFeatureFlags(res)
  })
})

onMounted(() => {
  WindowSetTitle(`go-stock：AI赋能股票分析✨ ${officialStatement.value} [数据来源于网络，仅供参考；投资有风险，入市需谨慎]`)
  contentStyle.value = 'max-height: calc(92vh);overflow: hidden'
  GetConfig().then((res) => {
    syncFeatureFlags(res)
  })
})
</script>
<template>
  <n-config-provider ref="containerRef" :theme="enableDarkTheme" :locale="zhCN" :date-locale="dateZhCN">
    <n-message-provider>
      <n-notification-provider>
        <n-modal-provider>
          <n-dialog-provider>
            <n-watermark
                :content="''"
                cross
                selectable
                :font-size="16"
                :line-height="16"
                :width="500"
                :height="400"
                :x-offset="50"
                :y-offset="150"
                :rotate="-15"
            >
              <n-flex>
                <n-grid x-gap="12" :cols="1">
                  <n-gi>
                    <n-spin :show="loading">
                      <template #description>
                        {{ loadingMsg }}
                      </template>
                      <n-scrollbar :style="contentStyle">
                        <n-skeleton v-if="loading" height="calc(100vh)" />
                        <RouterView/>
                      </n-scrollbar>
                    </n-spin>
                  </n-gi>
                  <n-gi style="position: fixed;bottom:0;z-index: 9;width: 100%;">
                    <n-card size="small" style="--wails-draggable:no-drag">
                      <n-menu style="font-size: 18px;"
                              v-model:value="activeKey"
                              mode="horizontal"
                              :options="menuOptions"
                              responsive
                      />
                    </n-card>
                  </n-gi>
                </n-grid>
              </n-flex>
            </n-watermark>
          </n-dialog-provider>
        </n-modal-provider>
      </n-notification-provider>
    </n-message-provider>
  </n-config-provider>
</template>
<style>

</style>
