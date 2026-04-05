<script setup lang="ts">
import {onBeforeMount, ref} from 'vue'
import {GetStockList, StockNotice} from "../services/app-api";
import {BrowserOpenURL} from "../../wailsjs/runtime";
import {RefreshCircleSharp} from "@vicons/ionicons5";
import _ from "lodash";
import KLineChart from "./KLineChart.vue";
import MoneyTrend from "./moneyTrend.vue";
import {useMessage} from "naive-ui";

const {stockCode}=defineProps(
    {
      stockCode: {
        type: String,
        default: ''
      }
    }
)

const list  = ref([])
const options =  ref([])
const message=useMessage()
function getNotice(stockCodes) {
  StockNotice(stockCodes).then(result => {
    console.log(result)
    list.value = result
  })
}

onBeforeMount (()=>{
  //message.info("正在获取数据"+stockCode)
  getNotice(stockCode);
})

function findStockList(query){
  if (query){
    GetStockList(query).then(result => {
      options.value=result.map(item => {
        return {
          label: item.name+" - "+item.ts_code,
          value: item.ts_code
        }
      })
    })
  }else{
    getNotice("")
  }
}
function handleSearch(value) {
  getNotice(value)
}
function openWin(code) {
  BrowserOpenURL("https://pdf.dfcfw.com/pdf/H2_"+code+"_1.pdf?1750092081000.pdf")
}
function getTypeColor(name){
  if(name.includes("质押")||name.includes("冻结")||name.includes("解冻")||name.includes("解押")||name.includes("解禁")){
    return "error"
  }
  if(name.includes("异常")||name.includes("减持")||name.includes("增发")||name.includes("重大")){
    return "error"
  }
  if(name.includes("季度报告")||name.includes("年度报告")||name.includes("澄清公告")||name.includes("风险")){
    return "error"
  }
  if(name.includes("终止")||name.includes("复牌")||name.includes("停牌")||name.includes("退市")){
    return "error"
  }
  if(name.includes("破产")||name.includes("清算")){
    return "error"
  }
  if(name.includes("回购")||name.includes("重组")||name.includes("诉讼")||name.includes("仲裁")||name.includes("转让")||name.includes("收购")){
    return "warning"
  }
  if(name.includes("调研")||name.includes("募集")){
    return "warning"
  }

  return "info"

}
function getmMarketCode(market,code) {
  if(market==="0"){
    return "sz"+code
  }else if(market==="1"){
    return "sh"+code
  }else if(market==="2"){
    return "bj"+code
  }else if(market==="3"){
    return "hk"+code
  }else{
    return code
  }
}
</script>

<template>
  <n-card>
    <n-auto-complete  :options="options" placeholder="请输入A股名称或者代码"  clearable filterable  :on-select="handleSearch" :on-update:value="findStockList"  />
  </n-card>
  <n-table striped size="small">
    <n-thead>
      <n-tr>
        <n-th>股票代码</n-th>
        <n-th>股票名称</n-th>
        <n-th>公告标题</n-th>
        <n-th>公告类型</n-th>
        <n-th>公告日期</n-th>
        <n-th><n-flex>数据更新时间<n-icon @click="getNotice('')" color="#409EFF" :size="20"  :component="RefreshCircleSharp"/></n-flex></n-th>
      </n-tr>
    </n-thead>
    <n-tbody>
      <n-tr v-for="item in list" :key="item.art_code">
        <n-td>
          <n-popover trigger="hover" placement="right">
            <template #trigger>
              <n-tag type="info"  :bordered="false">{{item.codes[0].stock_code }}</n-tag>
            </template>
            <money-trend style="width: 800px" :code="getmMarketCode(item.codes[0].market_code,item.codes[0].stock_code)" :name="item.codes[0].short_name"  :days="360" :dark-theme="true" :chart-height="500"></money-trend>
          </n-popover>
        </n-td>
        <n-td>
          <n-popover trigger="hover" placement="right">
            <template #trigger>
              <n-tag type="info"  :bordered="false">{{item.codes[0].short_name }}</n-tag>
            </template>
            <k-line-chart style="width: 800px" :code="getmMarketCode(item.codes[0].market_code,item.codes[0].stock_code)" :chart-height="500" :stockName="item.codes[0].short_name" :k-days="20" :dark-theme="true"></k-line-chart>
          </n-popover>
        </n-td>
        <n-td>
          <n-a type="info"  @click="openWin(item.art_code)"><n-text  :type="getTypeColor(item.columns[0].column_name)"> {{item.title}}</n-text></n-a>
        </n-td>
        <n-td>
          <n-text :type="getTypeColor(item.columns[0].column_name)">{{item.columns[0].column_name }}</n-text>
        </n-td>
        <n-td>
          <n-tag type="info">{{item.notice_date.substring(0,10) }}</n-tag>
        </n-td>
        <n-td>
          <n-tag type="info">{{item.display_time.substring(0,19)}}</n-tag>
        </n-td>
      </n-tr>
    </n-tbody>
  </n-table>
</template>

<style scoped>

</style>
