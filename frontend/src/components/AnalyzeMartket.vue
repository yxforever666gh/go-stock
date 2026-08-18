<script setup>

import {AnalyzeSentimentWithFreqWeight,GlobalStockIndexes} from "../services/market-api";
import * as echarts from "echarts";
import {onMounted,onUnmounted, ref} from "vue";
import _ from "lodash";
const { name,darkTheme,kDays ,chartHeight} = defineProps({
  name: {
    type: String,
    default: ''
  },
  kDays: {
    type: Number,
    default: 14
  },
  chartHeight: {
    type: Number,
    default: 500
  },
  darkTheme: {
    type: Boolean,
    default: false
  }
})
const common = ref([])
const america = ref([])
const europe = ref([])
const asia = ref([])
const mainIndex = ref([])
const chinaIndex = ref([])
const other = ref([])
const globalStockIndexes = ref(null)
const chartRef = ref(null);
const gaugeChartRef = ref(null);
const triggerAreas=ref(["main","extra","arrow"])
let handleChartInterval=null
let handleIndexInterval=null
onMounted(() => {
  handleChart()
  getIndex()
  handleChartInterval=setInterval(function () {
    handleChart()
  }, 1000 * 60)

  handleIndexInterval=setInterval(function () {
    getIndex()
  }, 1000 * 2)
})

onUnmounted(()=>{
  clearInterval(handleChartInterval)
  clearInterval(handleIndexInterval)
})

function getIndex() {
  GlobalStockIndexes().then((res) => {
    globalStockIndexes.value = res
    common.value = res["common"]
    america.value = res["america"]
    europe.value = res["europe"]
    asia.value = res["asia"]
    other.value = res["other"]
    mainIndex.value=asia.value.filter(function (item) {
      return ['上海',"深圳","香港","台湾","北京","东京","首尔","纽约","纳斯达克"].includes(item.location)
    }).concat(america.value.filter(function (item) {
      return ['上海',"深圳","香港","台湾","北京","东京","首尔","纽约","纳斯达克"].includes(item.location)
    }))

    chinaIndex.value=asia.value.filter(function (item) {
      return ['上海',"深圳","香港","台湾","北京"].includes(item.location)
    })

  })
}
function  handleChart(){
  const formatUtil = echarts.format;
  AnalyzeSentimentWithFreqWeight("").then((res) => {
    const treemapchart = echarts.init(chartRef.value);
    const gaugeChart=echarts.init(gaugeChartRef.value);
    let data = res['frequencies'].map(item => ({
      name: item.Word,
      // value: item.Frequency,
      // value: item.Weight,
      frequency: item.Frequency,
      weight: item.Weight,
      value: item.Score,
    }));

    let data2 = res['frequencies'].map(item => ({
      name: item.Word,
       value: item.Frequency,
      // value: item.Weight,
      frequency: item.Frequency,
      weight: item.Weight,
      //value: item.Score,
    }));

    let data3 = res['frequencies'].map(item => ({
      name: item.Word,
      //value: item.Frequency,
       value: item.Weight,
      frequency: item.Frequency,
      weight: item.Weight,
      //value: item.Score,
    }));

    let option = {
      darkMode: darkTheme,
      title: {
        text:name,
        left: 'center',
        textStyle: {
          color: darkTheme?'#ccc':'#456'
        }
      },
      legend: {
        show: false
      },
      toolbox: {
        left: '20px',
        tooltip:{
          textStyle: {
            color: darkTheme?'#ccc':'#456'
          }
        },
        feature: {
          saveAsImage: {title: '保存图片'},
          restore: {
            title: '默认',
          },
          myTool2: {
            show: true,
            title: '按权重',
            icon:"path://M393.8816 148.1216a29.3376 29.3376 0 0 1-15.2576 38.0928c-43.776 17.152-81.92 43.8272-114.2784 76.2368A345.7536 345.7536 0 0 0 159.5392 512 352.8704 352.8704 0 0 0 512 864.4608a351.744 351.744 0 0 0 249.5488-102.912 353.536 353.536 0 0 0 76.2368-114.2784c5.6832-15.2576 22.8352-20.992 38.0928-15.2576 15.2576 5.7344 20.992 22.8864 15.2576 38.0928a421.2224 421.2224 0 0 1-89.6 133.376A412.6208 412.6208 0 0 1 512 921.6c-226.7136 0-409.6-182.8864-409.6-409.6 0-108.544 41.9328-211.456 120.0128-289.5872A421.2224 421.2224 0 0 1 355.84 132.864a29.3376 29.3376 0 0 1 38.0928 15.2576zM512 102.4c226.7136 0 409.6 182.8864 409.6 409.6 0 15.2576-13.312 28.5696-28.5696 28.5696H512A29.2864 29.2864 0 0 1 483.4304 512V130.9696c0-15.2576 13.312-28.5696 28.5696-28.5696z m28.5696 59.0336v321.9968h321.9968a350.976 350.976 0 0 0-321.9968-321.9968z",
            onclick: function (){
              treemapchart.setOption( {series:{
                  data: data3
                }})
            }
          },
          myTool1: {
            show: true,
            title: '按频次',
            icon:"path://M895.466667 476.8l-87.424-87.424v-123.626667a49.770667 49.770667 0 0 0-49.770667-49.770666h-123.626667L547.2 128.533333a49.792 49.792 0 0 0-70.4 0l-87.424 87.424h-123.626667a49.770667 49.770667 0 0 0-49.770666 49.770667v123.626667L128.533333 476.8a49.792 49.792 0 0 0 0 70.4l87.424 87.424v123.626667a49.770667 49.770667 0 0 0 49.770667 49.770666h123.626667l87.424 87.424a49.792 49.792 0 0 0 70.4 0l87.424-87.424h123.626666a49.770667 49.770667 0 0 0 49.770667-49.770666v-123.626667l87.424-87.424a49.749333 49.749333 0 0 0 0.042667-70.4z m-137.216 137.194667v144.256h-144.256L512 860.266667l-101.994667-101.994667h-144.256v-144.256L163.733333 512l101.994667-101.994667v-144.256h144.256L512 163.733333l101.994667 101.994667h144.256v144.256L860.266667 512l-102.016 101.994667z M414.378667 514.730667l28.672 10.922666c-18.090667 47.445333-38.229333 92.16-60.757334 133.802667l-30.037333-13.653333a1042.133333 1042.133333 0 0 0 62.122667-131.072zM381.952 367.616L355.669333 384c25.258667 26.282667 45.056 50.176 60.074667 72.021333l25.6-17.749333c-13.994667-20.48-33.792-44.032-59.392-70.656zM537.258667 455.338667c-0.682667 43.690667-6.144 79.189333-16.725334 106.837333-14.336 32.768-44.373333 60.416-89.429333 82.944l21.162667 25.941333c52.224-26.624 85.333333-60.074667 99.328-100.693333 1.706667-5.12 3.413333-10.24 4.778666-15.36 21.504 45.738667 52.906667 83.968 93.866667 115.370667l21.504-24.917334c-51.2-34.474667-86.357333-81.237333-105.813333-140.288 1.706667-15.701333 2.730667-32.085333 2.730666-49.834666h-31.402666z M508.586667 434.858667h115.712c-6.826667 25.258667-15.018667 47.786667-24.917334 66.901333l31.744 8.874667a627.008 627.008 0 0 0 27.989334-85.674667v-21.162667H517.12c3.413333-14.336 6.144-29.354667 8.874667-45.738666l-32.426667-5.12c-7.850667 59.392-25.6 105.813333-52.906667 139.264l26.965334 19.114666c16.725333-19.114667 30.378667-44.373333 40.96-76.458666z",
            onclick: function (){
              treemapchart.setOption( {series:{
                  data: data2
                }})
            }
          }
        }
      },
      tooltip: {
        formatter: function (info) {
          var value = info.value.toFixed(2);
          var frequency = info.data.frequency;
          var weight = info.data.weight;
          return [
            '<div class="tooltip-title">' + info.name+ '</div>',
            '热度: ' + formatUtil.addCommas(value) + '',
            '<div class="tooltip-title">频次: ' +  formatUtil.addCommas(frequency)+ '</div>',
            '<div class="tooltip-title">权重: ' +  formatUtil.addCommas(weight)+ '</div>',
          ].join('');
        }
      },
      series: [
        {
          type: 'treemap',
          breadcrumb:{show: false},
          left: '0',
          top: '40',
          right: '0',
          bottom: '0',
          tooltip: {
            show: true
          },
          data: data
        }
      ]
    };
    treemapchart.setOption(option);



    let option2 = {
      darkMode: darkTheme,
      series: [
        {
          type: 'gauge',
          startAngle: 180,
          endAngle: 0,
          center: ['50%', '75%'],
          radius: '90%',
          min: -100,
          max: 100,
          splitNumber: 8,
          axisLine: {
            lineStyle: {
              width: 6,
              color: [
                // [0.25, '#FF6E76'],
                // [0.5, '#FDDD60'],
                // [0.75, '#58D9F9'],
                // [1, '#7CFFB2'],

                [0.25, '#03fb6a'],
                [0.5, '#58e1f9'],
                [0.75, '#ef5922'],
                [1, '#f11d29'],

              ]
            }
          },
          pointer: {
            icon: 'path://M12.8,0.7l12,40.1H0.7L12.8,0.7z',
            length: '12%',
            width: 20,
            offsetCenter: [0, '-60%'],
            itemStyle: {
              color: 'auto'
            }
          },
          axisTick: {
            length: 12,
            lineStyle: {
              color: 'auto',
              width: 2
            }
          },
          splitLine: {
            length: 20,
            lineStyle: {
              color: 'auto',
              width: 5
            }
          },
          axisLabel: {
            color:  darkTheme?'#ccc':'#456',
            fontSize: 20,
            distance: -45,
            rotate: 'tangential',
            formatter: function (value) {
              if (value ===100) {
                return '极热';
              } else if (value === 50) {
                return '乐观';
              }  else if (value === 0) {
                return '中性';
              }else if (value === -50) {
                return '谨慎';
              } else if (value === -100) {
                return '冰点';
              }
              return '';
            }
          },
          title: {
            offsetCenter: [0, '-10%'],
            fontSize: 20
          },
          detail: {
            fontSize: 30,
            offsetCenter: [0, '-35%'],
            valueAnimation: true,
            formatter: function (value) {
              return value.toFixed(2) + '';
            },
            color: 'inherit'
          },
          data: [
            {
              value: res.result.Score*0.2,
              name: '市场情绪强弱'
            }
          ]
        }
      ]
    };
    gaugeChart.setOption(option2);
  })
}
</script>

<template>
  <n-collapse :trigger-areas="triggerAreas" :default-expanded-names="['1']" display-directive="show">
    <n-collapse-item  name="1" >
      <template #header>
          <n-flex>
              <n-tag size="small" :bordered="false" v-for="(item, index) in mainIndex" :type="item.zdf>0?'error':'success'">
                <n-flex>
                  <n-image :width="20" :src="item.img" />
                  <n-text style="font-size: 14px" :type="item.zdf>0?'error':'success'">{{item.name}}&nbsp;{{item.zxj}}</n-text>
                  <n-number-animation :precision="2" :from="0" :to="item.zdf" style="font-size: 14px"/>
                  <n-text style="margin-left: -12px;font-size: 14px" :type="item.zdf>0?'error':'success'">%</n-text>
                </n-flex>
              </n-tag>
          </n-flex>
      </template>
      <template #header-extra>
        主要股指
      </template>
      <n-grid :cols="24" :y-gap="0">
        <n-gi span="6">
          <div ref="gaugeChartRef" style="width: 100%;height: auto" :style="{height:chartHeight+'px'}" ></div>
        </n-gi>
        <n-gi span="18">
          <div ref="chartRef" style="width: 100%;height: auto" :style="{height:chartHeight+'px'}" ></div>
        </n-gi>
      </n-grid>
    </n-collapse-item>
  </n-collapse>
</template>

<style scoped>

</style>
