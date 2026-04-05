<script setup>

import {CaretDown, CaretUp, RefreshCircleOutline} from "@vicons/ionicons5";
import {NText,useMessage} from "naive-ui";
import {onBeforeUnmount, onMounted, onUnmounted, ref} from "vue";
import {GetMoneyRankSina} from "../services/app-api";
import KLineChart from "./KLineChart.vue";

const props = defineProps({
  headerTitle: {
    type: String,
    default: '净流入额排名'
  },
  sort: {
    type: String,
    default: 'netamount'
  },
})
const message = useMessage()
const dataList= ref([])
const sort = ref(props.sort)
const interval = ref(null)
onMounted(()=>{
  sort.value=props.sort
  GetMoneyRankSinaData()
  interval.value=setInterval(()=>{
    GetMoneyRankSinaData()
  },1000*60)
})
onBeforeUnmount(()=>{
  clearInterval(interval.value)
})
function GetMoneyRankSinaData(){
  message.loading("正在刷新数据...")
  GetMoneyRankSina(sort.value).then(result => {
    if(result.length>0){
      dataList.value = result
    }
  })
}
</script>

<template>
  <n-table striped size="small">
    <n-thead>
      <n-tr>
        <n-th>代码</n-th>
        <n-th>名称</n-th>
        <n-th>最新价</n-th>
        <n-th>涨跌幅</n-th>
        <n-th>换手率</n-th>
        <n-th>成交额/万</n-th>
        <n-th>流出资金/万</n-th>
        <n-th>流入资金/万</n-th>
        <n-th>净流入/万</n-th>
        <n-th>净流入率</n-th>
        <n-th v-if="sort === 'r0_net'||sort==='r0_out'">主力流出/万</n-th>
        <n-th v-if="sort === 'r0_net'">主力流入/万</n-th>
        <n-th v-if="sort === 'r0_net'">主力净流入/万</n-th>
        <n-th >主力净流入率</n-th>
        <n-th v-if="sort === 'r3_net'||sort==='r3_out'">散户流出/万</n-th>
        <n-th v-if="sort === 'r3_net'">散户流入/万</n-th>
        <n-th v-if="sort === 'r3_net'">散户净流入/万</n-th>
        <n-th >散户净流入率</n-th>
      </n-tr>
    </n-thead>
    <n-tbody>
      <n-tr v-for="item in dataList" :key="item.symbol">
        <n-td><n-tag :bordered=false type="info">{{ item.symbol }}</n-tag></n-td>
        <n-td>
          <n-popover trigger="hover" placement="right">
            <template #trigger>
              <n-button tag="a"  text :type="item.changeratio>0?'error':'success'" :bordered=false >{{ item.name }}</n-button>
            </template>
            <k-line-chart style="width: 800px" :code="item.symbol" :chart-height="500" :stockName="item.name" :k-days="20" :dark-theme="true"></k-line-chart>
          </n-popover>
        </n-td>
        <n-td><n-text :type="item.changeratio>0?'error':'success'">{{item.trade}}</n-text></n-td>
        <n-td><n-text :type="item.changeratio>0?'error':'success'">{{(item.changeratio*100).toFixed(2)}}%</n-text></n-td>
        <n-td><n-text :type="item.turnover>500?'error':'info'">{{(item.turnover/100).toFixed(2)}}%</n-text></n-td>
        <n-td><n-text type="info">{{(item.amount/10000).toFixed(2)}}</n-text></n-td>
        <n-td><n-text type="info"> {{(item.outamount/10000).toFixed(2)}}</n-text></n-td>
        <n-td><n-text type="info"> {{(item.inamount/10000).toFixed(2)}}</n-text></n-td>
        <n-td><n-text type="info"> {{(item.netamount/10000).toFixed(2)}}</n-text></n-td>
        <n-td><n-text :type="item.ratioamount>0?'error':'success'"> {{(item.ratioamount*100).toFixed(2)}}%</n-text></n-td>
        <n-td v-if="sort === 'r0_net'||sort==='r0_out'"><n-text  type="success"> {{(item.r0_out/10000).toFixed(2)}}</n-text></n-td>
        <n-td v-if="sort === 'r0_net'"><n-text  type="error"> {{(item.r0_in/10000).toFixed(2)}}</n-text></n-td>
        <n-td v-if="sort === 'r0_net'"><n-text :type="item.r0_net>0?'error':'success'"> {{(item.r0_net/10000).toFixed(2)}}</n-text></n-td>
        <n-td ><n-text :type="item.r0_ratio>0?'error':'success'"> {{(item.r0_ratio*100).toFixed(2)}}%</n-text></n-td>
        <n-td v-if="sort === 'r3_net'||sort==='r3_out'"><n-text  type="success"> {{(item.r3_out/10000).toFixed(2)}}</n-text></n-td>
        <n-td v-if="sort === 'r3_net'"><n-text  type="error"> {{(item.r3_in/10000).toFixed(2)}}</n-text></n-td>
        <n-td v-if="sort === 'r3_net'"><n-text :type="item.r3_net>0?'error':'success'"> {{(item.r3_net/10000).toFixed(2)}}</n-text></n-td>
        <n-td ><n-text :type="item.r3_ratio>0?'error':'success'"> {{(item.r3_ratio*100).toFixed(2)}}%</n-text></n-td>
      </n-tr>
    </n-tbody>
  </n-table>
</template>

<style scoped>

</style>
