<script setup>
import { CaretDown, CaretUp } from '@vicons/ionicons5'
import IndustryMoneyRank from '../components/industryMoneyRank.vue'

defineProps({
  industryRanks: {
    type: Array,
    default: () => [],
  },
  sort: {
    type: String,
    default: '0',
  },
})

const emit = defineEmits(['toggle-sort'])

function handleToggleSort() {
  emit('toggle-sort')
}
</script>

<template>
  <n-tabs type="card" animated>
    <n-tab-pane name="行业涨幅排名" tab="行业涨幅排名">
      <n-table striped>
        <n-thead>
          <n-tr>
            <n-th>行业名称</n-th>
            <n-th @click="handleToggleSort">行业涨幅
              <n-icon v-if="sort === '0'" :component="CaretDown" />
              <n-icon v-if="sort === '1'" :component="CaretUp" />
            </n-th>
            <n-th>行业5日涨幅</n-th>
            <n-th>行业20日涨幅</n-th>
            <n-th>领涨股</n-th>
            <n-th>涨幅</n-th>
            <n-th>最新价</n-th>
          </n-tr>
        </n-thead>
        <n-tbody>
          <n-tr v-for="item in industryRanks" :key="item.bd_code">
            <n-td>
              <n-tag :bordered="false" type="info">{{ item.bd_name }}</n-tag>
            </n-td>
            <n-td>
              <n-text :type="item.bd_zdf > 0 ? 'error' : 'success'">{{ item.bd_zdf }}%</n-text>
            </n-td>
            <n-td>
              <n-text :type="item.bd_zdf5 > 0 ? 'error' : 'success'">{{ item.bd_zdf5 }}%</n-text>
            </n-td>
            <n-td>
              <n-text :type="item.bd_zdf20 > 0 ? 'error' : 'success'">{{ item.bd_zdf20 }}%</n-text>
            </n-td>
            <n-td>
              <n-text :type="item.nzg_zdf > 0 ? 'error' : 'success'">
                {{ item.nzg_name }}
                <n-text type="info">{{ item.nzg_code }}</n-text>
              </n-text>
            </n-td>
            <n-td>
              <n-text :type="item.nzg_zdf > 0 ? 'error' : 'success'">{{ item.nzg_zdf }}%</n-text>
            </n-td>
            <n-td>
              <n-text :type="item.nzg_zdf > 0 ? 'error' : 'success'">{{ item.nzg_zxj }}</n-text>
            </n-td>
          </n-tr>
        </n-tbody>
      </n-table>
    </n-tab-pane>
    <n-tab-pane name="行业资金排名(净流入)" tab="行业资金排名">
      <IndustryMoneyRank :fenlei="'0'" :header-title="'行业资金排名(净流入)'" :sort="'netamount'" />
    </n-tab-pane>
    <n-tab-pane name="证监会行业资金排名(净流入)" tab="证监会行业资金排名">
      <IndustryMoneyRank :fenlei="'2'" :header-title="'证监会行业资金排名(净流入)'" :sort="'netamount'" />
    </n-tab-pane>
    <n-tab-pane name="概念板块资金排名(净流入)" tab="概念板块资金排名">
      <IndustryMoneyRank :fenlei="'1'" :header-title="'概念板块资金排名(净流入)'" :sort="'netamount'" />
    </n-tab-pane>
  </n-tabs>
</template>
