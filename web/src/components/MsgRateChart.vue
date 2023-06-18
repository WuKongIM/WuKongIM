<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watchEffect } from 'vue';
import * as echarts from "echarts";
import { formatDate, formatNumber } from '@/services/Utils';
const props = defineProps({
  upstream: {
    type: Array<number>,
    required: true
  },
  downstream: {
    type: Array<number>,
    required: true
  },
})

const connChart = ref<any>(null);


const option = ref({
  title: {
    text: '消息速率(条/秒)',
    textStyle: {
      fontWeight: 'normal',
      fontSize: 12
    }
  },
  tooltip: {
    show: true,
    trigger: 'axis',
    formatter: function (params: any) {
      var res = formatDate(new Date(params[0].name * 1000)) + '<br/>'
      for (var i = 0, length = params.length; i < length; i++) {
        res += params[i].marker + params[i].seriesName + '：'
          + formatNumber(params[i].value) + '<br/>'
      }
      return res

    }
  },
  legend: [
    {
      data: ['上行数据包', '下行数据包'],
      bottom: '0%',
      textStyle: {
        fontSize: 12,
      },
    },
  ],
  grid: {
    left: "1%",
    top: "15%",
    bottom: "15%",
    containLabel: true,
  },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    axisLabel: {
      formatter: function (value: number, index: number) {
        const date = new Date(value * 1000);
        return formatDate(date)
      }
    },
    data: new Array<number>(),
  },
  yAxis: {
    type: 'value',
    minInterval: 1,
    axisLabel: {
      formatter: function (value: number, index: number) {
        return formatNumber(value);
      }
    }

  },
  series: [
    {
      name: '上行数据包',
      type: 'line',
      symbol: 'none',
      data: new Array<number>(),
    },
    {
      name: '下行数据包',
      type: 'line',
      symbol: 'none',
      data: new Array<number>(),
    },
  ],
});
const updateData = async (last: boolean) => {

  let currentTimestamp = Math.floor(Date.now() / 1000);
  for (let i = 0; i < props.upstream.length; i++) {
    if (!last) {
      currentTimestamp -= 1
    }
    const value = props.upstream[i];
    if (option.value.xAxis.data.length > 60) {
      option.value.xAxis.data.shift();
      option.value.series[0].data.shift();
      option.value.series[1].data.shift();
    }
    option.value.xAxis.data.push(currentTimestamp);
    option.value.series[0].data.push(props.upstream[i]);
    option.value.series[1].data.push(props.downstream[i]);

  }
}
const refreshChart = () => {
  chart?.setOption(option.value);
}

let chart: echarts.ECharts
// 待组件挂载完成后初始化 ECharts
onMounted(() => {


  chart = echarts.init(connChart.value);
  chart.setOption(option.value);

  // startRealtimeData();
  updateData(false)
  refreshChart()

  window.addEventListener('resize', () => chart.resize())

})

watchEffect(() => {
  updateData(props.upstream.length == 1)
  refreshChart()
});





onBeforeUnmount(() => {
  window.removeEventListener('resize', () => chart.resize())
  if (chart) {
    chart.dispose()
  }
})

</script>


<template>
  <div class="h-full w-full" ref="connChart"></div>
</template>