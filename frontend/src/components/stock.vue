<script setup>
import {computed, defineAsyncComponent, h, nextTick, onBeforeMount, onBeforeUnmount, onMounted, reactive, ref, watch} from 'vue'
import {
  AddGroup,
  AddStockGroup,
  Follow,
  GetAiConfigs,
  GetAIResponseResult,
  GetConfig,
  GetFollowList,
  GetGroupList,
  GetPromptTemplates,
  GetStockList,
  GetVersionInfo,
  Greet,
  InitializeGroupSort,
  NewChatStream,
  OpenURL,
  RemoveGroup,
  RemoveStockGroup,
  SaveAIResponseResult,
  SaveAsMarkdown,
  SetAlarmChangePercent,
  SetCostPriceAndVolume,
  SetStockAICron,
  SetStockSort,
  ShareAnalysis,
  UnFollow,
  UpdateGroupSort
} from '../services/app-api'
import {
  NAvatar,
  NButton,
  NFlex,
  NForm,
  NFormItem,
  NInputNumber,
  NText,
  useDialog,
  useMessage,
  useNotification
} from 'naive-ui'
import {
  Environment,
  EventsEmit,
  EventsOff,
  EventsOn,
  WindowFullscreen,
  WindowReload,
  WindowUnfullscreen
} from '../../wailsjs/runtime'
import {Add} from '@vicons/ionicons5'
import '@vavt/v3-extension/lib/asset/ExportPDF.css';
import {useRoute, useRouter} from 'vue-router'
import MoneyTrend from "./moneyTrend.vue";
import StockSparkLine from "./stockSparkLine.vue";
import { useStockHeavyFeatures } from '../composables/useStockHeavyFeatures'

const MdEditor = defineAsyncComponent(() => import('md-editor-v3').then((mod) => mod.MdEditor))
const MdPreview = defineAsyncComponent(() => import('md-editor-v3').then((mod) => mod.MdPreview))
const ExportPDF = defineAsyncComponent(() => import('@vavt/v3-extension').then((mod) => mod.ExportPDF))

const route = useRoute()
const router = useRouter()

const dialog = useDialog()
const toolbars = [0];

const upColor = '#ec0000';
const upBorderColor = '';
const downColor = '#00da3c';
const downBorderColor = '';
const kLineChartRef = ref(null);
const kLineChartRef2 = ref(null);


const handleProgress = (progress) => {
  //console.log(`Export progress: ${progress.ratio * 100}%`);
};
const enableEditor = ref(false)
const mdPreviewRef = ref(null)
const mdEditorRef = ref(null)
const tipsRef = ref(null)
const message = useMessage()
const notify = useNotification()
const stocks = ref([])
const results = ref({})
const stockList = ref([])
const followList = ref([])
const groupList = ref([])
const options = ref([])
const modalShow = ref(false)
const modalShow2 = ref(false)
const modalShow3 = ref(false)
const modalShow4 = ref(false)
const modalShow5 = ref(false)
const addBTN = ref(true)
const enableTools = ref(true)
const thinkingMode = ref(true)
const formModel = ref({
  name: "",
  code: "",
  costPrice: 0.000,
  volume: 0,
  alarm: 0,
  alarmPrice: 0,
  sort: 999,
  cron: "",
})

const promptTemplates = ref([])
const aiConfigs = ref([])
const sysPromptOptions = ref([])
const userPromptOptions = ref([])
const data = reactive({
  modelName: "",
  chatId: "",
  question: "",
  sysPromptId: null,
  aiConfigId: null,
  name: "",
  code: "",
  fenshiURL: "",
  kURL: "",
  resultText: "Please enter your name below 👇",
  fullscreen: false,
  airesult: "",
  openAiEnable: false,
  loading: true,
  darkTheme: false,
  changePercent: 0
})
const feishiInterval = ref(null)
const {
  exportAnalysisAsCanvasImage,
  exportAnalysisAsImage,
  exportAnalysisAsWord,
  renderDailyKLine,
  renderMinuteChart,
} = useStockHeavyFeatures({
  data,
  downColor,
  kLineChartRef,
  kLineChartRef2,
  mdPreviewRef,
  message,
  tipsRef,
  upColor,
})


const currentGroupId = ref(0)


const theme = computed(() => {
  return data.darkTheme ? 'dark' : 'light'
})

const danmakuColor = computed(() => {
  return data.darkTheme ? 'color:#fff' : 'color:#000'
})

const icon = ref('');

const sortedResults = computed(() => {
  const sortedKeys = Object.keys(results.value).sort();
  const sortedObject = {};
  sortedKeys.forEach(key => {
    sortedObject[key] = results.value[key];
  });
  return sortedObject
});

const groupResults = computed(() => {
  const group = {}
  if (currentGroupId.value === 0) {
    return sortedResults.value
  } else {
    for (const key in sortedResults.value) {
      if (stocks.value.includes(sortedResults.value[key]['股票代码'])) {
        group[key] = sortedResults.value[key]
      }
    }
    return group
  }
})
const showPopover = ref(false)
// 拖拽相关变量
const dragSourceIndex = ref(null)
const dragTargetIndex = ref(null)

// 拖拽处理函数
function handleTabDragStart(event, name) {
  // "全部"标签（name=0）不应该触发拖拽
  if (name === 0) {
    event.preventDefault();
    return;
  }
  dragSourceIndex.value = name;
  event.dataTransfer.effectAllowed = 'move';
  event.target.classList.add('tab-dragging');
}


function handleTabDragOver(event) {
  event.preventDefault()
  event.dataTransfer.dropEffect = 'move'
}

function handleTabDragEnter(event, name) {
  event.preventDefault();
  // "全部"标签（name=0）不应该作为拖拽目标
  if (name > 0) {
    dragTargetIndex.value = name;
    if (event.target.classList) {
      // 查找最近的标签元素并添加高亮样式
      let tabElement = event.target.closest('.n-tabs-tab');
      if (tabElement) {
        tabElement.classList.add('tab-drag-over');
      }
    }
  }
}

function handleTabDragLeave(event) {
  // 查找最近的标签元素并移除高亮样式
  let tabElement = event.target.closest('.n-tabs-tab')
  if (tabElement && tabElement.classList) {
    tabElement.classList.remove('tab-drag-over')
  }
  // 不要重置 dragTargetIndex，因为可能会在元素间快速移动
}

function handleTabDrop(event) {
  event.preventDefault();

  // 移除所有高亮样式
  const tabs = document.querySelectorAll('.n-tabs-tab');
  tabs.forEach(tab => {
    tab.classList.remove('tab-drag-over');
  });

  if (dragSourceIndex.value !== null && dragTargetIndex.value !== null &&
      dragSourceIndex.value !== dragTargetIndex.value) {

    // 确保索引有效（排除"全部"选项卡）
    if (dragSourceIndex.value > 0 && dragTargetIndex.value > 0) {
      // 查找源分组和目标分组
      const sourceGroup = groupList.value.find(g => g.ID === dragSourceIndex.value);
      const targetGroup = groupList.value.find(g => g.ID === dragTargetIndex.value);

      if (sourceGroup && targetGroup) {
        // 计算新的位置序号（使用目标分组的sort值）
        const newSortPosition = targetGroup.sort;

        // 调用后端API更新组排序
        UpdateGroupSort(sourceGroup.ID, newSortPosition).then(result => {
          if (result) {
            message.success('分组排序更新成功');
            // 重新获取分组列表以更新界面
            GetGroupList().then(result => {
              groupList.value = result;
            });
          } else {
            message.error('分组排序更新失败');
          }
        }).catch(error => {
          message.error('分组排序更新失败: ' + error.message);
        });
      }
    }
  }

  // 重置状态
  dragSourceIndex.value = null;
  dragTargetIndex.value = null;
}

function handleTabDragEnd(event) {
  // 移除所有高亮样式
  const tabs = document.querySelectorAll('.n-tabs-tab')
  tabs.forEach(tab => {
    tab.classList.remove('tab-drag-over', 'tab-dragging')
  })

  dragSourceIndex.value = null
  dragTargetIndex.value = null
}

onBeforeMount(() => {
  GetGroupList().then(result => {
    groupList.value = result
    // 检查是否存在相同的序号
    const sorts = result.map(item => item.sort);
    const uniqueSorts = new Set(sorts);
    // 如果存在重复的序号，则重新初始化序号
    if (sorts.length !== uniqueSorts.size) {
      // 调用InitializeGroupSort重新初始化序号
      // 然后重新获取分组列表
      fetchGroupList();
    } else {
      // 没有重复序号，继续正常流程
      if (route.query.groupId) {
        message.success("切换分组:" + route.query.groupName)
        currentGroupId.value = Number(route.query.groupId)
      }
    }
  })
  GetStockList("").then(result => {
    stockList.value = result
    options.value = result.map(item => {
      return {
        label: item.name + " - " + item.ts_code,
        value: item.ts_code
      }
    })
  })
  GetConfig().then(result => {
    if (result.openAiEnable) {
      data.openAiEnable = true
    }
    if (result.darkTheme) {
      data.darkTheme = true
    }
  })
  GetPromptTemplates("", "").then(res => {
    promptTemplates.value = res

    sysPromptOptions.value = promptTemplates.value.filter(item => item.type === '模型系统Prompt')
    userPromptOptions.value = promptTemplates.value.filter(item => item.type === '模型用户Prompt')

  })

  GetAiConfigs().then(res => {
    aiConfigs.value = res
    data.aiConfigId = res[0].ID
  })

  EventsOn("loadingDone", (data) => {
    message.loading("刷新股票基础数据...")
    GetStockList("").then(result => {
      stockList.value = result
      options.value = result.map(item => {
        return {
          label: item.name + " - " + item.ts_code,
          value: item.ts_code
        }
      })
    })
  })

  EventsOn("refresh", (data) => {
    message.success(data)
  })

  EventsOn("showSearch", (data) => {
    addBTN.value = data === 1;
  })

  EventsOn("stock_price", (data) => {
    updateData(data)
  })

  EventsOn("refreshFollowList", (data) => {

    WindowReload()
  })

  EventsOn("newChatStream", async (msg) => {
    data.loading = false
    if (msg === "DONE") {
      SaveAIResponseResult(data.code, data.name, data.airesult, data.chatId, data.question, data.aiConfigId)
      message.info("AI分析完成！")
      message.destroyAll()
    } else {
      if (msg.aiConfigId) {
        data.aiConfigId = msg.aiConfigId
      }
      if (msg.chatId) {
        data.chatId = msg.chatId
      }
      if (msg.question) {
        data.question = msg.question
      }
      if (msg.content) {
        data.airesult = data.airesult + msg.content
      }
      if (msg.extraContent) {
        data.airesult = data.airesult + msg.extraContent
      }

    }
  })

  EventsOn("changeTab", async (msg) => {
    currentGroupId.value = Number(msg.ID)
    nextTick(() => {
      updateTab(currentGroupId.value);
    });
  })


  EventsOn("updateVersion", async (msg) => {
    const githubTimeStr = msg.published_at;
    // 创建一个 Date 对象
    const utcDate = new Date(githubTimeStr);
// 获取本地时间
    const date = new Date(utcDate.getTime());
    const year = date.getFullYear();
// getMonth 返回值是 0 - 11，所以要加 1
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
            src: icon.value
          }),
      title: '发现新版本: ' + msg.tag_name,
      content: () => {
        //return h(MdPreview, {theme:'dark',modelValue:msg.commit?.message}, null)
        return h('div', {
          style: {
            'text-align': 'left',
            'font-size': '14px',
          }
        }, {default: () => msg.commit?.message})
      },
      duration: 5000,
      meta: "发布时间:" + formattedDate,
      action: () => {
        return h(NButton, {
          type: 'primary',
          size: 'small',
          onClick: () => {
            Environment().then(env => {
              switch (env.platform) {
                case 'windows':
                  window.open(msg.html_url)
                  break
                default :
                  OpenURL(msg.html_url)
              }
            })
          }
        }, {default: () => '查看'})
      }
    })
  })

  EventsOn("warnMsg", async (msg) => {
    notify.error({
      avatar: () =>
          h(NAvatar, {
            size: 'small',
            round: false,
            src: icon.value
          }),
      title: '警告',
      duration: 5000,
      content: () => {
        return h('div', {
          style: {
            'text-align': 'left',
            'font-size': '14px',
          }
        }, {default: () => msg})
      },
    })
  })
})

onMounted(() => {
  nextTick(() => {
    initDraggableTabs();
  });

  // 监听分组列表变化，重新初始化拖拽
  const unwatch = watch(groupList, () => {
    nextTick(() => {
      initDraggableTabs();
    });
  });

  // 在组件卸载时清理监听器
  onBeforeUnmount(() => {
    unwatch();
  });
  message.loading("Loading...")
  GetFollowList(currentGroupId.value).then(result => {

    followList.value = result
    for (const followedStock of result) {
      if (followedStock.StockCode.startsWith("us")) {
        followedStock.StockCode = "gb_" + followedStock.StockCode.replace("us", "").toLowerCase()
      }
      if (!stocks.value.includes(followedStock.StockCode)) {
        stocks.value.push(followedStock.StockCode)
      }
      Greet(followedStock.StockCode).then(result => {
        updateData(result)
      })
    }
    //monitor()
    message.destroyAll()
  })

  GetVersionInfo().then((res) => {
    icon.value = res.icon;
  });
})
// 清理拖拽事件监听器
// 清理拖拽事件监听器
function cleanupDraggableTabs() {
  const tabs = document.querySelectorAll('.n-tabs-tab');
  tabs.forEach((tab) => {
    // 移除所有可能的拖拽事件监听器
    tab.removeEventListener('dragstart', handleTabDragStart);
    tab.removeEventListener('dragover', handleTabDragOver);
    tab.removeEventListener('dragenter', handleTabDragEnter);
    tab.removeEventListener('dragleave', handleTabDragLeave);
    tab.removeEventListener('drop', handleTabDrop);
    tab.removeEventListener('dragend', handleTabDragEnd);
    // 移除draggable属性
    tab.removeAttribute('draggable');
  });
}

// 初始化可拖拽选项卡
function initDraggableTabs() {
  // 移除之前可能添加的事件监听器
  cleanupDraggableTabs();

  // 添加拖拽事件监听器到选项卡元素
  setTimeout(() => {
    const tabs = document.querySelectorAll('.n-tabs-tab');
    tabs.forEach((tab, index) => {
      const dataIndex = tab.getAttribute('data-name');
      const name = parseInt(dataIndex);

      // 只为分组标签（name > 0）添加拖拽功能
      if (name > 0) {
        tab.setAttribute('draggable', 'true');
        tab.addEventListener('dragstart', (e) => handleTabDragStart(e, name));
        tab.addEventListener('dragover', handleTabDragOver);
        tab.addEventListener('dragenter', (e) => handleTabDragEnter(e, name));
        tab.addEventListener('dragleave', handleTabDragLeave);
        tab.addEventListener('drop', handleTabDrop);
        tab.addEventListener('dragend', handleTabDragEnd);
      }
    });
  }, 100);
}

onBeforeUnmount(() => {
  // //console.log(`the component is now unmounted.`)
  //clearInterval(ticker.value)
  message.destroyAll()
  notify.destroyAll()
  clearInterval(feishiInterval.value)

  EventsOff("refresh")
  EventsOff("showSearch")
  EventsOff("stock_price")
  EventsOff("refreshFollowList")
  EventsOff("newChatStream")
  EventsOff("changeTab")
  EventsOff("updateVersion")
  EventsOff("warnMsg")
  EventsOff("loadingDone")

  cleanupDraggableTabs()

})

//判断是否是A股交易时间
function isTradingTime() {
  const now = new Date();
  const day = now.getDay(); // 获取星期几，0表示周日，1-6表示周一至周六
  if (day >= 1 && day <= 5) { // 周一至周五
    const hours = now.getHours();
    const minutes = now.getMinutes();
    const totalMinutes = hours * 60 + minutes;
    const startMorning = 9 * 60 + 15; // 上午9点15分换算成分钟数
    const endMorning = 11 * 60 + 30; // 上午11点30分换算成分钟数
    const startAfternoon = 13 * 60; // 下午13点换算成分钟数
    const endAfternoon = 15 * 60; // 下午15点换算成分钟数
    if ((totalMinutes >= startMorning && totalMinutes < endMorning) ||
        (totalMinutes >= startAfternoon && totalMinutes < endAfternoon)) {
      return true;
    }
  }
  return false;
}

// 添加一个获取分组列表的函数，用于处理初始化逻辑
function fetchGroupList() {
  InitializeGroupSort().then(initResult => {
    if (initResult) {
      GetGroupList().then(result => {
        groupList.value = result
        if (route.query.groupId) {
          message.success("切换分组:" + route.query.groupName)
          currentGroupId.value = Number(route.query.groupId)
        }
      })
    } else {
      message.error("初始化分组序号失败")
    }
  })
}

function AddStock() {
  if (!data?.code) {
    message.error("请输入有效股票代码");
    return;
  }
  if (!stocks.value.includes(data.code)) {
    Follow(data.code).then(result => {
      if (result === "关注成功") {
        if (data.code.startsWith("us")) {
          data.code = "gb_" + data.code.replace("us", "").toLowerCase()
        }
        stocks.value.push(data.code)
        message.success(result)
        GetFollowList(currentGroupId.value).then(result => {
          followList.value = result
        })
        monitor();
      } else {
        message.error(result)
      }
    })
  } else {
    message.error("已经关注了")
  }
}


function removeMonitor(code, name, key) {
  //console.log("removeMonitor",name,code,key)
  stocks.value.splice(stocks.value.indexOf(code), 1)
  //console.log("removeMonitor-key",key)
  //console.log("removeMonitor-v",results.value[key])

  delete results.value[key]
  //console.log("removeMonitor-v",results.value[key])

  UnFollow(code).then(result => {
    message.success(result)
    monitor()
  })
}

function getStockList(value) {


  // //console.log("getStockList",value)
  let result;
  result = stockList.value.filter(item => item.name.includes(value) || item.ts_code.includes(value))
  options.value = result.map(item => {
    return {
      label: item.name + " - " + item.ts_code,
      value: item.ts_code
    }
  })
  if (value && value.indexOf("-") <= 0) {
    data.code = value
  }

  //console.log("getStockList-options",data.code)

  if (data.code) {
    let findId = data.code
    if (findId.startsWith("us")) {
      findId = "gb_" + findId.replace("us", "").toLowerCase()
    }
    blinkBorder(findId)
  }


}

function blinkBorder(findId) {
  // 获取要滚动到的元素
  let element = document.getElementById(findId);
  //console.log("blinkBorder",findId,element)
  if (element) {
    // 滚动到该元素
    element.scrollIntoView({behavior: 'smooth'});
    const pelement = document.getElementById(findId + '_gi');
    if (pelement) {
      // 添加闪烁效果
      pelement.classList.add('blink-border');
      // 3秒后移除闪烁效果
      setTimeout(() => {
        pelement.classList.remove('blink-border');
      }, 1000 * 5);
    } else {
      console.error(`Element with ID ${findId}_gi not found`);
    }
  }
}

async function updateData(result) {
  ////console.log("stock_price",result['日期'],result['时间'],result['股票代码'],result['股票名称'],result['当前价格'],result['盘前盘后'])

  if (result["当前价格"] <= 0) {
    result["当前价格"] = result["卖一报价"]
  }

  if (result.changePercent > 0) {
    result.type = "error"
    result.color = "#E88080"
  } else if (result.changePercent < 0) {
    result.type = "success"
    result.color = "#63E2B7"
  } else {
    result.type = "default"
    result.color = "#FFFFFF"
  }

  if (result.profitAmount > 0) {
    result.profitType = "error"
  } else if (result.profitAmount < 0) {
    result.profitType = "success"
  }
  if (result["当前价格"]) {
    if (result.alarmChangePercent > 0 && Math.abs(result.changePercent) >= result.alarmChangePercent) {
      SendMessage(result, 1)
    }

    if (result.alarmPrice > 0 && result["当前价格"] >= result.alarmPrice) {
      SendMessage(result, 2)
    }

    if (result.costPrice > 0 && result["当前价格"] >= result.costPrice) {
      SendMessage(result, 3)
    }
  }

  // result.key=result.sort
  results.value = Object.fromEntries(
      Object.entries(results.value).filter(
          ([key]) => !key.includes(result["股票代码"])
      ));

  result.key = GetSortKey(result.sort, result["股票代码"])
  results.value[result.key] = result
  if (!stocks.value.includes(result["股票代码"])) {
    delete results.value[result.key]
  }
}


async function monitor() {
  if (stocks.value && stocks.value.length === 0) {
    showPopover.value = true
  }
  for (let code of stocks.value) {
    Greet(code).then(result => {
      updateData(result)
    })
  }
}


function GetSortKey(sort, code) {
  return String(sort).padStart(8, '0') + "_" + code
}

function onSelect(item) {
  ////console.log("onSelect",item)

  if (item.indexOf("-") > 0) {
    item = item.split("-")[1].toLowerCase()
  }
  if (item.indexOf(".") > 0) {
    data.code = item.split(".")[1].toLowerCase() + item.split(".")[0]
  }

}

function openCenteredWindow(url, width, height) {
  const left = (window.screen.width - width) / 2;
  const top = (window.screen.height - height) / 2;
  Environment().then(env => {
    switch (env.platform) {
      case 'windows':
        window.open(
            url,
            'centeredWindow',
            `width=${width},height=${height},left=${left},top=${top},location=no,menubar=no,toolbar=no,display=standalone`
        )
        break
      default :
        OpenURL(url)
        break
    }
  })


  //
  // return window.open(
  //     url,
  //     'centeredWindow',
  //     `width=${width},height=${height},left=${left},top=${top}`
  // );
}

function search(code, name) {
  setTimeout(() => {
    //window.open("https://xueqiu.com/S/"+code)
    //window.open("https://www.cls.cn/stock?code="+code)
    //window.open("https://quote.eastmoney.com/"+code+".html")
    //window.open("https://finance.sina.com.cn/realstock/company/"+code+"/nc.shtml")
    //window.open("https://www.iwencai.com/unifiedwap/result?w=" + name)
    //window.open("https://www.iwencai.com/chat/?question="+code)

    openCenteredWindow("https://www.iwencai.com/unifiedwap/result?w=" + name, 1000, 800)

  }, 500)
}

function setStock(code, name) {
  let res = followList.value.filter(item => item.StockCode === code)
  ////console.log("res:",res)
  formModel.value.name = name
  formModel.value.code = code
  formModel.value.volume = res[0].Volume ? res[0].Volume : 0
  formModel.value.costPrice = res[0].CostPrice
  formModel.value.alarm = res[0].AlarmChangePercent
  formModel.value.alarmPrice = res[0].AlarmPrice
  formModel.value.sort = res[0].Sort
  formModel.value.cron = res[0].Cron
  modalShow.value = true
}

function clearFeishi() {
  //console.log("clearFeishi")
  clearInterval(feishiInterval.value)
}

function showFsChart(code, name) {
  return renderMinuteChart(code, name)
}

function showFenshi(code, name, changePercent) {
  data.code = code
  data.name = name
  data.changePercent = changePercent
  data.fenshiURL = 'http://image.sinajs.cn/newchart/min/n/' + data.code + '.gif' + "?t=" + Date.now()

  if (code.startsWith('hk')) {
    data.fenshiURL = 'http://image.sinajs.cn/newchart/hk_stock/min/' + data.code.replace("hk", "") + '.gif' + "?t=" + Date.now()
  }
  if (code.startsWith('gb_')) {
    data.fenshiURL = 'http://image.sinajs.cn/newchart/usstock/min/' + data.code.replace("gb_", "") + '.gif' + "?t=" + Date.now()
  }

  modalShow2.value = true
}

function handleFeishi() {
  showFsChart(data.code, data.name);
  feishiInterval.value = setInterval(() => {
    showFsChart(data.code, data.name);
  }, 1000 * 10)
}

function handleKLine() {
  return renderDailyKLine()
}

function showMoney(code, name) {
  data.code = code
  data.name = name
  modalShow5.value = true
}

function showK(code, name) {
  data.code = code
  data.name = name
  data.kURL = 'http://image.sinajs.cn/newchart/daily/n/' + data.code + '.gif' + "?t=" + Date.now()
  if (code.startsWith('hk')) {
    data.kURL = 'http://image.sinajs.cn/newchart/hk_stock/daily/' + data.code.replace("hk", "") + '.gif' + "?t=" + Date.now()
  }
  if (code.startsWith('gb_')) {
    data.kURL = 'http://image.sinajs.cn/newchart/usstock/daily/' + data.code.replace("gb_", "") + '.gif' + "?t=" + Date.now()
  }
  modalShow3.value = true
  //https://image.sinajs.cn/newchart/usstock/daily/dji.gif
  //https://image.sinajs.cn/newchart/hk_stock/daily/06030.gif?1740729404273
}


function updateCostPriceAndVolumeNew(code, price, volume, alarm, formModel) {
  if (formModel.sort) {
    SetStockSort(formModel.sort, code).then(result => {
      //message.success(result)
    })
  }
  if (formModel.cron) {
    SetStockAICron(formModel.cron, code).then(result => {
      //message.success(result)
    })
  }

  if (alarm || formModel.alarmPrice) {
    SetAlarmChangePercent(alarm, formModel.alarmPrice, code).then(result => {
      //message.success(result)
    })
  }
  SetCostPriceAndVolume(code, price, volume).then(result => {
    modalShow.value = false
    message.success(result)
    GetFollowList(currentGroupId.value).then(result => {
      followList.value = result
      stocks.value = []
      for (const followedStock of result) {
        if (!stocks.value.includes(followedStock.StockCode)) {
          stocks.value.push(followedStock.StockCode)
        }
      }
      monitor()
      message.destroyAll()
    })
  })
}

function fullscreen() {
  if (data.fullscreen) {
    WindowUnfullscreen()
  } else {
    WindowFullscreen()
  }
  data.fullscreen = !data.fullscreen
}


//type 报警类型: 1 涨跌报警;2 股价报警 3 成本价报警
function SendMessage(result, type) {
  return `${getTypeName(type)}:${result["股票代码"]}`
}

function aiReCheckStock(stock, stockCode) {
  data.modelName = ""
  data.airesult = ""
  data.time = ""
  data.name = stock
  data.code = stockCode
  data.loading = true
  modalShow4.value = true
  message.loading("ai检测中...", {
    duration: 0,
  })
  //

  //message.info("sysPromptId:"+data.sysPromptId)
  NewChatStream(stock, stockCode, data.question, data.aiConfigId, data.sysPromptId, enableTools.value,thinkingMode.value)
}

function aiCheckStock(stock, stockCode) {
  GetAIResponseResult(stockCode).then(result => {
    if (result.content) {
      data.modelName = result.modelName
      data.chatId = result.chatId
      data.question = result.question
      data.name = stock
      data.code = stockCode
      data.loading = false
      modalShow4.value = true
      data.airesult = result.content
      const date = new Date(result.CreatedAt);
      const year = date.getFullYear();
      const month = String(date.getMonth() + 1).padStart(2, '0');
      const day = String(date.getDate()).padStart(2, '0');
      const hours = String(date.getHours()).padStart(2, '0');
      const minutes = String(date.getMinutes()).padStart(2, '0');
      const seconds = String(date.getSeconds()).padStart(2, '0');
      data.time = `${year}-${month}-${day} ${hours}:${minutes}:${seconds}`
    } else {
      data.modelName = ""
      data.question = ""
      data.airesult = ""
      data.time = ""
      data.name = stock
      data.code = stockCode
      data.loading = false
      modalShow4.value = true
      // message.loading("ai检测中...", {
      //   duration: 0,
      // })
      // NewChatStream(stock, stockCode, "", data.sysPromptId)
    }
  })
}

function getTypeName(type) {
  switch (type) {
    case 1:
      return "涨跌报警"
    case 2:
      return "股价报警"
    case 3:
      return "成本价报警"
    default:
      return ""
  }
}

//获取高度
function getHeight() {
  return document.documentElement.clientHeight
}

window.onerror = function (msg, source, lineno, colno, error) {
  // 将错误信息发送给后端
  EventsEmit("frontendError", {
    page: "stock.vue",
    message: msg,
    source: source,
    lineno: lineno,
    colno: colno,
    error: error ? error.stack : null,
    data: data,
    results: results,
    followList: followList,
    stockList: stockList,
    stocks: stocks,
    formModel: formModel,
  });
  message.error("发生错误:" + msg)
  return true;
};

function saveAsImage(name, code) {
  return exportAnalysisAsImage(name, code)
}

async function saveCanvasImage(name) {
  return exportAnalysisAsCanvasImage(name)
}

async function copyToClipboard() {
  try {
    await navigator.clipboard.writeText(data.airesult);
    message.success('分析结果已复制到剪切板');
  } catch (err) {
    message.error('复制失败: ' + err);
  }
}

function saveAsMarkdown() {
  SaveAsMarkdown(data.code, data.name).then(result => {
    message.success(result)
  })
}

function saveAsMarkdown_old() {
  const blob = new Blob([data.airesult], {type: 'text/markdown;charset=utf-8'});
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `${data.name}[${data.code}]-${data.time}ai-analysis-result.md`;
  link.click();
  URL.revokeObjectURL(link.href);
  link.remove()
}

// 导出文档
async function saveAsWord() {
  return exportAnalysisAsWord()
}

function share(code, name) {
  ShareAnalysis(code, name).then(msg => {
    //message.info(msg)
    notify.info({
      avatar: () =>
          h(NAvatar, {
            size: 'small',
            round: false,
            src: icon.value
          }),
      title: '分享到社区',
      duration: 1000 * 30,
      content: () => {
        return h('div', {
          style: {
            'text-align': 'left',
            'font-size': '14px',
          }
        }, {default: () => msg})
      },
    })
  })
}

const addTabModel = ref({
  name: '',
  sort: 1,
})
const addTabPane = ref(false)

function addTab() {
  addTabPane.value = true
}

function saveTabPane() {
  AddGroup(addTabModel.value).then(result => {
    message.info(result)
    addTabPane.value = false
    GetGroupList().then(result => {
      groupList.value = result
    })
  })
}

function AddStockGroupInfo(groupId, code, name) {
  if (code.startsWith("gb_")) {
    code = "us" + code.replace("gb_", "").toLowerCase()
  }
  AddStockGroup(groupId, code).then(result => {
    message.info(result)
    GetGroupList().then(result => {
      groupList.value = result
    })
  })

}

function updateTab(name) {
  stocks.value = []
  const tabId= Number(name)
  currentGroupId.value = tabId;
  GetFollowList(tabId).then(result => {
    followList.value = result

    for (const followedStock of result) {
      if (followedStock.StockCode.startsWith("us")) {
        followedStock.StockCode = "gb_" + followedStock.StockCode.replace("us", "").toLowerCase()
      }
      stocks.value.push(followedStock.StockCode)
      Greet(followedStock.StockCode).then(result => {
        updateData(result)
      })
    }
    //monitor()
    message.destroyAll()
  })
}

function delTab(groupId) {
  let infos = groupList.value = groupList.value.filter(item => item.ID === Number(groupId))
  dialog.create({
    title: '删除分组',
    type: 'warning',
    content: '确定要删除[' + infos[0].name + ']分组吗？分组数据将不能恢复哟！',
    positiveText: '确定',
    negativeText: '取消',
    onPositiveClick: () => {
      RemoveGroup(Number(groupId)).then(result => {
        message.info(result)
        GetGroupList().then(result => {
          groupList.value = result
        })
      })
    }
  })
}

function delStockGroup(code, name, groupId) {
  RemoveStockGroup(code, name, groupId).then(result => {
    updateTab(groupId)
    message.info(result)
  })
}

function searchNotice(stockCode) {
  router.push({
    name: 'market',
    query: {
      name: '公司公告',
      stockCode: stockCode,
    },
  })
}

function searchStockReport(stockCode) {
  router.push({
    name: 'market',
    query: {
      name: '个股研报',
      stockCode: stockCode,
    },
  })
}
</script>

<template>
  <n-tabs type="card" style="--wails-draggable:no-drag" animated addable :data-currentGroupId="currentGroupId"
          :value="String(currentGroupId)" @add="addTab" @update:value="updateTab" placement="top" @close="(key)=>{delTab(key)}">

    <n-tab-pane closable name="0" :tab="'全部'">
      <n-grid :x-gap="8" :cols="3" :y-gap="8">
        <n-gi :id="result['股票代码']+'_gi'" v-for="result in sortedResults" style="margin-left: 2px;">
          <n-card :data-sort="result.sort" :id="result['股票代码']" :data-code="result['股票代码']" :bordered="true"
                  :title="result['股票名称']" :closable="false"
                  @close="removeMonitor(result['股票代码'],result['股票名称'],result.key)">
            <n-grid :cols="1" :y-gap="6">
              <n-gi>
                <n-text :type="result.type">
                  <n-number-animation :duration="1000" :precision="2" :from="result['上次当前价格']"
                                      :to="Number(result['当前价格'])"/>
                  <n-tag size="small" :type="result.type" :bordered="false" v-if="result['盘前盘后']>0">
                    ({{ result['盘前盘后'] }} {{ result['盘前盘后涨跌幅'] }}%)
                  </n-tag>
                </n-text>
                <n-text style="padding-left: 10px;" :type="result.type">
                  <n-number-animation :duration="1000" :precision="3" :from="0" :to="result.changePercent"/>
                  %
                </n-text>&nbsp;
                <n-text size="small" v-if="result.costVolume>0" :type="result.type">
                  <n-number-animation :duration="1000" :precision="2" :from="0" :to="result.profitAmountToday"/>
                </n-text>
              </n-gi>
            </n-grid>
            <n-grid :cols="2" :y-gap="4" :x-gap="4">
              <n-gi>
                <n-text :type="'info'">{{ "最高 " + result["今日最高价"] + " " + result.highRate }}%</n-text>
              </n-gi>
              <n-gi>
                <n-text :type="'info'">{{ "最低 " + result["今日最低价"] + " " + result.lowRate }}%</n-text>
              </n-gi>
              <n-gi>
                <n-text :type="'info'">{{ "昨收 " + result["昨日收盘价"] }}</n-text>
              </n-gi>
              <n-gi>
                <n-text :type="'info'">{{ "今开 " + result["今日开盘价"] }}</n-text>
              </n-gi>
            </n-grid>
            <n-collapse accordion v-if="result['买一报价']>0">
              <n-collapse-item title="盘口" name="1" v-if="result['买一报价']>0">
                <template #header-extra>
                  <n-flex justify="space-between">
                    <n-text :type="'info'">{{ "买一 " + result["买一报价"] + '(' + result["买一申报"] + ")" }}</n-text>
                    <n-text :type="'info'">{{ "卖一 " + result["卖一报价"] + '(' + result["卖一申报"] + ")" }}</n-text>
                  </n-flex>
                </template>
                <n-grid :cols="2" :y-gap="4" :x-gap="4">
                  <n-gi v-if="result['买一报价']>0">
                    <n-text :type="'info'">{{ "买一 " + result["买一报价"] + '(' + result["买一申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖一报价']>0">
                    <n-text :type="'info'">{{ "卖一 " + result["卖一报价"] + '(' + result["卖一申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买二报价']>0">
                    <n-text :type="'info'">{{ "买二 " + result["买二报价"] + '(' + result["买二申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖二报价']>0">
                    <n-text :type="'info'">{{ "卖二 " + result["卖二报价"] + '(' + result["卖二申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买三报价']>0">
                    <n-text :type="'info'">{{ "买三 " + result["买三报价"] + '(' + result["买三申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖三报价']>0">
                    <n-text :type="'info'">{{ "买三 " + result["卖三报价"] + '(' + result["卖三申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买四报价']>0">
                    <n-text :type="'info'">{{ "买四 " + result["买四报价"] + '(' + result["买四申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖四报价']>0">
                    <n-text :type="'info'">{{ "卖四 " + result["卖四报价"] + '(' + result["卖四申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买五报价']>0">
                    <n-text :type="'info'">{{ "买五 " + result["买五报价"] + '(' + result["买五申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖五报价']>0">
                    <n-text :type="'info'">{{ "卖五 " + result["卖五报价"] + '(' + result["卖五申报"] + ")" }}</n-text>
                  </n-gi>
                </n-grid>
              </n-collapse-item>
            </n-collapse>
            <template #header-extra>

              <n-tag size="small" :bordered="false">{{ result['股票代码'] }}</n-tag>&nbsp;
              <n-button size="tiny" secondary type="primary"
                        @click="removeMonitor(result['股票代码'],result['股票名称'],result.key)">
                取消关注
              </n-button>&nbsp;

              <n-button size="tiny" v-if="data.openAiEnable" secondary type="warning"
                        @click="aiCheckStock(result['股票名称'],result['股票代码'])">
                AI分析
              </n-button>
            </template>
            <template #footer>
              <n-flex justify="center">
                <n-text :type="'info'">{{ result["日期"] + " " + result["时间"] }}</n-text>
                <n-tag size="small" v-if="result.volume>0" :type="result.profitType">{{ result.volume + "股" }}</n-tag>
                <n-tag size="small" v-if="result.costPrice>0" :type="result.profitType">
                  {{
                    "成本:" + result.costPrice + "*" + result.costVolume + " " + result.profit + "%" + " ( " + result.profitAmount + " ¥ )"
                  }}
                </n-tag>
              </n-flex>
            </template>
            <template #action>
              <n-flex justify="left">
                <n-button size="tiny" type="warning" @click="setStock(result['股票代码'],result['股票名称'])"> 成本
                </n-button>
                <n-button size="tiny" type="error"
                          @click="showFenshi(result['股票代码'],result['股票名称'],result.changePercent)"> 分时
                </n-button>
                <n-button size="tiny" type="error" @click="showK(result['股票代码'],result['股票名称'])"> 日K</n-button>
                <n-button size="tiny" type="error" v-if="result['买一报价']>0"
                          @click="showMoney(result['股票代码'],result['股票名称'])"> 资金
                </n-button>
                <n-button size="tiny" type="success" @click="search(result['股票代码'],result['股票名称'])"> 详情
                </n-button>
                <n-button v-if="result['买一报价']>0" size="tiny" type="success"
                          @click="searchNotice(result['股票代码'])"> 公告
                </n-button>
                <n-button v-if="result['买一报价']>0" size="tiny" type="success"
                          @click="searchStockReport(result['股票代码'])"> 研报
                </n-button>
                <n-flex justify="right">
                  <n-dropdown trigger="click" :options="groupList" key-field="ID" label-field="name"
                              @select="(groupId) => AddStockGroupInfo(groupId,result['股票代码'],result['股票名称'])">
                    <n-button type="warning" size="tiny">设置分组</n-button>
                  </n-dropdown>
                </n-flex>
              </n-flex>
            </template>
          </n-card>
        </n-gi>
      </n-grid>
    </n-tab-pane>
    <n-tab-pane closable v-for="group in groupList" :group-id="group.ID" :name="String(group.ID)" :tab="group.name">
      <n-grid :x-gap="8" :cols="3" :y-gap="8">
        <n-gi :id="result['股票代码']+'_gi'" v-for="result in groupResults" style="margin-left: 2px;">
          <n-card :data-sort="result.sort" :id="result['股票代码']" :data-code="result['股票代码']" :bordered="true"
                  :title="result['股票名称']" :closable="false"
                  @close="removeMonitor(result['股票代码'],result['股票名称'],result.key)">
            <n-grid :cols="12" :y-gap="6">
              <n-gi :span="6">
                <n-text :type="result.type">
                  <n-number-animation :duration="1000" :precision="2" :from="result['上次当前价格']"
                                      :to="Number(result['当前价格'])"/>
                  <n-tag size="small" :type="result.type" :bordered="false" v-if="result['盘前盘后']>0">
                    ({{ result['盘前盘后'] }} {{ result['盘前盘后涨跌幅'] }}%)
                  </n-tag>
                </n-text>
                <n-text style="padding-left: 10px;" :type="result.type">
                  <n-number-animation :duration="1000" :precision="3" :from="0" :to="result.changePercent"/>
                  %
                </n-text>&nbsp;
                <n-text size="small" v-if="result.costVolume>0" :type="result.type">
                  <n-number-animation :duration="1000" :precision="2" :from="0" :to="result.profitAmountToday"/>
                </n-text>
              </n-gi>
              <n-gi :span="6">
                <stock-spark-line :last-price="Number(result['当前价格'])" :open-price="Number(result['昨日收盘价'])"
                                  :stock-code="result['股票代码']" :stock-name="result['股票名称']"></stock-spark-line>
              </n-gi>
            </n-grid>
            <n-grid :cols="2" :y-gap="4" :x-gap="4">
              <n-gi>
                <n-text :type="'info'">{{ "最高 " + result["今日最高价"] + " " + result.highRate }}%</n-text>
              </n-gi>
              <n-gi>
                <n-text :type="'info'">{{ "最低 " + result["今日最低价"] + " " + result.lowRate }}%</n-text>
              </n-gi>
              <n-gi>
                <n-text :type="'info'">{{ "昨收 " + result["昨日收盘价"] }}</n-text>
              </n-gi>
              <n-gi>
                <n-text :type="'info'">{{ "今开 " + result["今日开盘价"] }}</n-text>
              </n-gi>
            </n-grid>
            <n-collapse accordion v-if="result['买一报价']>0">
              <n-collapse-item title="盘口" name="1" v-if="result['买一报价']>0">
                <template #header-extra>
                  <n-flex justify="space-between">
                    <n-text :type="'info'">{{ "买一 " + result["买一报价"] + '(' + result["买一申报"] + ")" }}</n-text>
                    <n-text :type="'info'">{{ "卖一 " + result["卖一报价"] + '(' + result["卖一申报"] + ")" }}</n-text>
                  </n-flex>
                </template>
                <n-grid :cols="2" :y-gap="4" :x-gap="4">
                  <n-gi v-if="result['买一报价']>0">
                    <n-text :type="'info'">{{ "买一 " + result["买一报价"] + '(' + result["买一申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖一报价']>0">
                    <n-text :type="'info'">{{ "卖一 " + result["卖一报价"] + '(' + result["卖一申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买二报价']>0">
                    <n-text :type="'info'">{{ "买二 " + result["买二报价"] + '(' + result["买二申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖二报价']>0">
                    <n-text :type="'info'">{{ "卖二 " + result["卖二报价"] + '(' + result["卖二申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买三报价']>0">
                    <n-text :type="'info'">{{ "买三 " + result["买三报价"] + '(' + result["买三申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖三报价']>0">
                    <n-text :type="'info'">{{ "买三 " + result["卖三报价"] + '(' + result["卖三申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买四报价']>0">
                    <n-text :type="'info'">{{ "买四 " + result["买四报价"] + '(' + result["买四申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖四报价']>0">
                    <n-text :type="'info'">{{ "卖四 " + result["卖四报价"] + '(' + result["卖四申报"] + ")" }}</n-text>
                  </n-gi>

                  <n-gi v-if="result['买五报价']>0">
                    <n-text :type="'info'">{{ "买五 " + result["买五报价"] + '(' + result["买五申报"] + ")" }}</n-text>
                  </n-gi>
                  <n-gi v-if="result['卖五报价']>0">
                    <n-text :type="'info'">{{ "卖五 " + result["卖五报价"] + '(' + result["卖五申报"] + ")" }}</n-text>
                  </n-gi>
                </n-grid>
              </n-collapse-item>
            </n-collapse>
            <template #header-extra>

              <n-tag size="small" :bordered="false">{{ result['股票代码'] }}</n-tag>&nbsp;
              <n-button size="tiny" secondary type="primary"
                        @click="removeMonitor(result['股票代码'],result['股票名称'],result.key)">
                取消关注
              </n-button>&nbsp;

              <n-button size="tiny" v-if="data.openAiEnable" secondary type="warning"
                        @click="aiCheckStock(result['股票名称'],result['股票代码'])">
                AI分析
              </n-button>
              <n-button secondary type="error" size="tiny"
                        @click="delStockGroup(result['股票代码'],result['股票名称'],group.ID)">移出分组
              </n-button>
            </template>
            <template #footer>
              <n-flex justify="center">
                <n-text :type="'info'">{{ result["日期"] + " " + result["时间"] }}</n-text>
                <n-tag size="small" v-if="result.volume>0" :type="result.profitType">{{ result.volume + "股" }}</n-tag>
                <n-tag size="small" v-if="result.costPrice>0" :type="result.profitType">
                  {{
                    "成本:" + result.costPrice + "*" + result.costVolume + " " + result.profit + "%" + " ( " + result.profitAmount + " ¥ )"
                  }}
                </n-tag>
              </n-flex>
            </template>
            <template #action>
              <n-flex justify="left">
                <n-button size="tiny" type="warning" @click="setStock(result['股票代码'],result['股票名称'])"> 成本
                </n-button>
                <n-button size="tiny" type="error"
                          @click="showFenshi(result['股票代码'],result['股票名称'],result.changePercent)"> 分时
                </n-button>
                <n-button size="tiny" type="error" @click="showK(result['股票代码'],result['股票名称'])"> 日K</n-button>
                <n-button size="tiny" type="error" v-if="result['买一报价']>0"
                          @click="showMoney(result['股票代码'],result['股票名称'])"> 资金
                </n-button>
                <n-button size="tiny" type="success" @click="search(result['股票代码'],result['股票名称'])"> 详情
                </n-button>
                <n-button v-if="result['买一报价']>0" size="tiny" type="success"
                          @click="searchNotice(result['股票代码'])"> 公告
                </n-button>
                <n-button v-if="result['买一报价']>0" size="tiny" type="success"
                          @click="searchStockReport(result['股票代码'])"> 研报
                </n-button>
                <n-flex justify="right">
                  <n-dropdown trigger="click" :options="groupList" key-field="ID" label-field="name"
                              @select="(groupId) => AddStockGroupInfo(groupId,result['股票代码'],result['股票名称'])">
                    <n-button type="warning" size="tiny">设置分组</n-button>
                  </n-dropdown>
                </n-flex>
              </n-flex>
            </template>
          </n-card>
        </n-gi>
      </n-grid>
    </n-tab-pane>
  </n-tabs>

  <div style="position: fixed;bottom: 18px;right:5px;z-index: 10;width: 400px">
    <!--    <n-card :bordered="false">-->
    <n-input-group>
      <!--        <n-button  type="error" @click="addBTN=!addBTN" > <n-icon :component="Search"/>&nbsp;<n-text  v-if="addBTN">隐藏</n-text></n-button>-->

      <n-auto-complete v-model:value="data.name" v-if="addBTN"
                       :input-props="{
                                autocomplete: 'disabled',
                              }"
                       :options="options"
                       placeholder="股票指数名称/代码"
                       clearable @update-value="getStockList" :on-select="onSelect"/>

      <n-popover trigger="manual" :show="showPopover">
        <template #trigger>
          <n-button type="primary" @click="AddStock" v-if="addBTN">
            <n-icon :component="Add"/> &nbsp;关注
          </n-button>
        </template>
        <span>输入股票名称/代码关键词开始吧~~~</span>
      </n-popover>
    </n-input-group>
    <!--    </n-card>-->
  </div>
  <n-modal transform-origin="center" size="small" v-model:show="modalShow" :title="formModel.name" style="width: 400px"
           :preset="'card'">
    <n-form :model="formModel" :rules="{
              costPrice: { required: true, message: '请输入成本'},
              volume: { required: true, message: '请输入数量'},
              alarm:{required: true, message: '涨跌报警值'} ,
              alarmPrice: { required: true, message: '请输入报警价格'},
              sort: { required: true, message: '请输入排序值'},
            }" label-placement="left" label-width="80px">
      <n-form-item label="股票成本" path="costPrice">
        <n-input-number v-model:value="formModel.costPrice" min="0" placeholder="请输入股票成本">
          <template #suffix>
            {{ formModel.code.indexOf("hk") >= 0 ? "HK$" : "¥" }}
          </template>
        </n-input-number>
      </n-form-item>
      <n-form-item label="股票数量" path="volume">
        <n-input-number v-model:value="formModel.volume" min="0" step="100" placeholder="请输入股票数量">
          <template #suffix>
            股
          </template>
        </n-input-number>
      </n-form-item>
      <n-form-item label="涨跌提醒" path="alarm">
        <n-input-number v-model:value="formModel.alarm" min="0" placeholder="请输入涨跌报警值(%)">
          <template #suffix>
            %
          </template>
        </n-input-number>
      </n-form-item>
      <n-form-item label="股价提醒" path="alarmPrice">
        <n-input-number v-model:value="formModel.alarmPrice" min="0" placeholder="请输入股价报警值(¥)">
          <template #suffix>
            {{ formModel.code.indexOf("hk") >= 0 ? "HK$" : "¥" }}
          </template>
        </n-input-number>
      </n-form-item>
      <n-form-item label="股票排序" path="sort">
        <n-input-number v-model:value="formModel.sort" min="0" placeholder="请输入股价排序值">
        </n-input-number>
      </n-form-item>
      <n-form-item label="AI cron" path="cron">
        <n-input v-model:value="formModel.cron" placeholder="请输入cron表达式"/>
      </n-form-item>
    </n-form>
    <template #footer>
      <n-button type="primary"
                @click="updateCostPriceAndVolumeNew(formModel.code,formModel.costPrice,formModel.volume,formModel.alarm,formModel)">
        保存
      </n-button>
    </template>
  </n-modal>

  <n-modal v-model:show="addTabPane" title="添加分组" style="width: 400px;text-align: left" :preset="'card'">
    <n-form
        :model="addTabModel"
        size="medium"
        label-placement="left"
    >
      <n-grid :cols="2">
        <n-form-item-gi label="分组名称:" path="name" :span="5">
          <n-input v-model:value="addTabModel.name" style="width: 100%" placeholder="请输入分组名称"/>
        </n-form-item-gi>
        <n-form-item-gi label="分组排序:" path="sort" :span="5">
          <n-input-number v-model:value="addTabModel.sort" style="width: 100%" min="0"
                          placeholder="请输入分组排序值"></n-input-number>
        </n-form-item-gi>
      </n-grid>
    </n-form>
    <template #footer>
      <n-flex justify="end">
        <n-button type="primary" @click="saveTabPane">
          保存
        </n-button>
        <n-button type="warning" @click="addTabPane=false">
          取消
        </n-button>
      </n-flex>
    </template>
  </n-modal>
  <n-modal v-model:show="modalShow2" :title="data.name+' '+ data.changePercent+'%'" style="width: 1000px"
           :preset="'card'" @after-enter="handleFeishi" @after-leave="clearFeishi">
    <!--    <n-image :src="data.fenshiURL" />-->
    <div ref="kLineChartRef2" style="width: 1000px; height: 500px;"></div>
  </n-modal>
  <n-modal v-model:show="modalShow3" :title="data.name" style="width: 1000px" :preset="'card'"
           @after-enter="handleKLine">
    <!--    <n-image :src="data.kURL" />-->
    <div ref="kLineChartRef" style="width: 1000px; height: 500px;"></div>
  </n-modal>

  <n-modal transform-origin="center" v-model:show="modalShow4" preset="card" style="width: 800px;"
           :title="'['+data.name+']AI分析'">
    <n-spin size="small" :show="data.loading">
      <MdEditor v-if="enableEditor" :toolbars="toolbars" ref="mdEditorRef" style="height: 440px;text-align: left"
                :modelValue="data.airesult" :theme="theme">
        <template #defToolbars>
          <ExportPDF :file-name="data.name+'['+data.code+']AI分析报告'" style="text-align: left"
                     :modelValue="data.airesult" @onProgress="handleProgress"/>
        </template>
      </MdEditor>
      <MdPreview v-if="!enableEditor" ref="mdPreviewRef" style="height: 440px;text-align: left"
                 :modelValue="data.airesult" :theme="theme"/>
    </n-spin>
    <template #footer>
      <n-flex justify="space-between" ref="tipsRef">
        <n-text type="info" v-if="data.time">
          <n-tag v-if="data.modelName" type="warning" round :title="data.chatId" :bordered="false">
            {{ data.modelName }}
          </n-tag>
          {{ data.time }}
        </n-text>
        <n-text type="error">*AI分析结果仅供参考，请以实际行情为准。投资需谨慎，风险自担。</n-text>
      </n-flex>
    </template>
    <template #action>
      <n-flex justify="left" style="margin-bottom: 10px">
        <n-switch v-model:value="enableTools" :round="false">
          <template #checked>
            启用AI函数工具调用
          </template>
          <template #unchecked>
            不启用AI函数工具调用
          </template>
        </n-switch>
        <n-switch v-model:value="thinkingMode" :round="false">
          <template #checked>
            启用思考模式
          </template>
          <template #unchecked>
            不启用思考模式
          </template>
        </n-switch>
        <n-gradient-text type="error" style="margin-left: 10px">
          *AI函数工具调用可以增强AI获取数据的能力,但会消耗更多tokens。
        </n-gradient-text>
      </n-flex>
      <n-flex justify="space-between" style="margin-bottom: 10px">
        <n-select style="width: 31%" v-model:value="data.aiConfigId" label-field="name" value-field="ID"
                  :options="aiConfigs" placeholder="请选择AI模型服务配置"/>
        <n-select style="width: 31%" v-model:value="data.sysPromptId" label-field="name" value-field="ID"
                  :options="sysPromptOptions" placeholder="请选择系统提示词"/>
        <n-select style="width: 31%" v-model:value="data.question" label-field="name" value-field="content"
                  :options="userPromptOptions" placeholder="请选择用户提示词"/>
      </n-flex>
      <n-flex justify="right">
        <n-input v-model:value="data.question" style="text-align: left" clearable
                 type="textarea"
                 :show-count="true"
                 placeholder="请输入您的问题:例如{{stockName}}[{{stockCode}}]分析和总结"
                 :autosize="{
              minRows: 2,
              maxRows: 5
            }"
        />
        <!--        <n-button size="tiny" type="error" @click="enableEditor=!enableEditor">编辑/预览</n-button>-->
        <n-button size="tiny" type="warning" @click="aiReCheckStock(data.name,data.code)">开始AI分析</n-button>
        <n-button size="tiny" type="info" @click="saveAsImage(data.name,data.code)">保存为图片</n-button>
        <n-button size="tiny" type="success" @click="copyToClipboard">复制到剪切板</n-button>
        <n-button size="tiny" type="primary" @click="saveAsMarkdown">保存为Markdown文件</n-button>
        <n-button size="tiny" type="primary" @click="saveAsWord">保存为Word文件</n-button>
        <n-button size="tiny" type="error" @click="share(data.code,data.name)">分享到项目社区</n-button>
      </n-flex>
    </template>
  </n-modal>
  <n-modal v-model:show="modalShow5" :title="data.name+'资金趋势'" style="width: 1000px" :preset="'card'">
    <money-trend :code="data.code" :name="data.name" :days="360" :dark-theme="data.darkTheme"
                 :chart-height="500"></money-trend>
  </n-modal>
</template>

<style scoped>
.md-editor-preview h3 {
  text-align: center !important;
}

.md-editor-preview p {
  text-align: left !important;
}

/* 添加闪烁效果的CSS类 */
.blink-border {
  animation: blink-border 1s linear infinite;
  border: 4px solid transparent;
}

@keyframes blink-border {
  0% {
    border-color: red;
  }
  50% {
    border-color: transparent;
  }
  100% {
    border-color: red;
  }
}

/* 所有标签的通用样式 */
:deep(.n-tabs-nav .n-tabs-tab) {
  position: relative;
  cursor: pointer;
}

/* 可拖拽标签的样式 */
:deep(.n-tabs-nav .n-tabs-tab[draggable="true"]) {
  user-select: none;
  cursor: move;
}

.tab-drag-over {
  background-color: #e6f7ff !important;
  border: 2px dashed #1890ff !important;
  transform: scale(1.02);
  transition: all 0.2s ease;
  z-index: 10;
}

.tab-drag-over::after {
  content: "";
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: -1;
}

.tab-dragging {
  opacity: 0.5;
}
</style>
