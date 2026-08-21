<script setup lang="ts">
import {onBeforeMount, ref} from 'vue'
import {HotEvent} from "../services/market-api";
import {usePolling} from "../composables/usePolling";
const list  = ref([])
const polling = usePolling(async () => {
  list.value = await HotEvent(50)
}, 1000 * 10)
onBeforeMount(() => polling.start())
</script>

<template>
  <n-list bordered>
    <template #header>
      雪球热门
    </template>
    <n-list-item v-for="(item, index) in list" :key="index">
        <n-thing :title="item.tag" :description="item.content"  >
          <template v-if="item.pic" #avatar>
            <n-avatar :src="item.pic" :size="60">
            </n-avatar>
          </template>
        </n-thing>
    </n-list-item>
  </n-list>
</template>

<style scoped>

</style>
