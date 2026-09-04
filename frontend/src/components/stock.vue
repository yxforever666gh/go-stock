<script setup>
import {computed, h, nextTick, onBeforeMount, onBeforeUnmount, onMounted, reactive, ref, watch} from 'vue'
import {
  AddGroup,
  AddStockGroup,
  GetGroupList,
  InitializeGroupSort,
  RemoveGroup,
  RemoveStockGroup,
  UpdateGroupSort
} from '../services/groups-api'
import {
  Follow,
  GetFollowList,
  GetStockList,
  Greet,
  SetAlarmChangePercent,
  SetCostPriceAndVolume,
  SetStockSort,
  UnFollow
} from '../services/stocks-api'
import {GetConfig} from '../services/settings-api'
import {GetVersionInfo} from '../services/system-api'
import {
  NAvatar,
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
  BrowserOpenURL as OpenURL,
  Environment,
  EventsEmit,
  EventsOff,
  EventsOn,
  WindowFullscreen,
  WindowReload,
  WindowUnfullscreen
} from '../services/browser-runtime.mjs'
import {Add} from '@vicons/ionicons5'
import {useRoute, useRouter} from 'vue-router'
import MoneyTrend from "./moneyTrend.vue";
import StockSparkLine from "./stockSparkLine.vue";
import { useDraggableGroupTabs } from '../composables/useDraggableGroupTabs'
import ProfessionalKLinePanel from './chart/ProfessionalKLinePanel.vue'

const route = useRoute()
const router = useRouter()

const dialog = useDialog()

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
const modalShow5 = ref(false)
const addBTN = ref(true)
const formModel = ref({
  name: "",
  code: "",
  costPrice: 0.000,
  volume: 0,
  alarm: 0,
  alarmPrice: 0,
  sort: 999,
})
const data = reactive({
  name: "",
  code: "",
  fenshiURL: "",
  kURL: "",
  resultText: "Please enter your name below 👇",
  fullscreen: false,
  loading: true,
  darkTheme: false,
  changePercent: 0
})
const currentGroupId = ref(0)


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
const {initialize: initDraggableTabs, cleanup: cleanupDraggableTabs} = useDraggableGroupTabs(
    groupList,
    async (sourceGroup, newSortPosition) => {
      try {
        const result = await UpdateGroupSort(sourceGroup.ID, newSortPosition)
        if (!result) throw new Error('后端未更新排序')
        message.success('分组排序更新成功')
        groupList.value = await GetGroupList()
      } catch (error) {
        message.error('分组排序更新失败: ' + error.message)
      }
    },
)

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
    if (result.darkTheme) {
      data.darkTheme = true
    }
  })

  // Clean up any stale event listeners (defensive)
  EventsOff("loadingDone")
  EventsOff("refresh")
  EventsOff("showSearch")
  EventsOff("stock_price")
  EventsOff("refreshFollowList")
  EventsOff("changeTab")
  EventsOff("warnMsg")

  // Register event listeners
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


  EventsOn("changeTab", async (msg) => {
    currentGroupId.value = Number(msg.ID)
    nextTick(() => {
      updateTab(currentGroupId.value);
    });
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
onBeforeUnmount(() => {
  // //console.log(`the component is now unmounted.`)
  //clearInterval(ticker.value)
  message.destroyAll()
  notify.destroyAll()
  EventsOff("refresh")
  EventsOff("showSearch")
  EventsOff("stock_price")
  EventsOff("refreshFollowList")
  EventsOff("changeTab")
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
  modalShow.value = true
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
  <n-tabs type="card" animated addable :data-currentGroupId="currentGroupId"
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
  <n-modal v-model:show="modalShow2" :title="data.name+' '+ data.changePercent+'%'" style="width: 1100px"
           :preset="'card'">
    <ProfessionalKLinePanel
        v-if="modalShow2"
        :code="data.code"
        :name="data.name"
        asset-type="stock"
        initial-period="1m"
        initial-adjustment="none"
        initial-view-mode="line"
        :chart-height="520"
        :dark-theme="data.darkTheme"
    />
  </n-modal>
  <n-modal v-model:show="modalShow3" :title="data.name" style="width: 1100px" :preset="'card'">
    <ProfessionalKLinePanel
        v-if="modalShow3"
        :code="data.code"
        :name="data.name"
        asset-type="stock"
        initial-period="day"
        initial-adjustment="qfq"
        :chart-height="520"
        :dark-theme="data.darkTheme"
    />
  </n-modal>

  <n-modal v-model:show="modalShow5" :title="data.name+'资金趋势'" style="width: 1000px" :preset="'card'">
    <money-trend :code="data.code" :name="data.name" :days="360" :dark-theme="data.darkTheme"
                 :chart-height="500"></money-trend>
  </n-modal>
</template>

<style scoped>
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
