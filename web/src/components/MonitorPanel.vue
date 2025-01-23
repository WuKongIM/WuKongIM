<script setup lang="ts">
import {  onMounted, ref, watchEffect } from 'vue';
import * as echarts from "echarts";
import { formatDate, formatNumber } from '../services/Utils';
import { Series } from '../services/Model';


const props = defineProps({
    data: {
        type: Array<Series>,
        required: true
    },
    title: {
        type: String,
    }
})

const chartElement = ref<any>(null);

const option = ref({
    title: {
        text: props.title || '',
        textStyle: {
            fontWeight: 'normal',
            fontSize: 12,
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
    legend: new Array<any>(),
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
            formatter: function (value: number) {
                const date = new Date(value * 1000);
                const hours = date.getHours();
                const minutes = date.getMinutes();
                const seconds = date.getSeconds();
                return hours + ':' + minutes + ':' + seconds;
            }
        },
        data: new Array<number>(),
    },
    yAxis: {
        boundaryGap: [0, '50%'],
        type: 'value',
        minInterval: 1,
        axisLabel: {
            formatter: function (value: number) {
                return formatNumber(value,0);
            }
        }

    },
    series: [
        {
            data: new Array<number>()
        },
    ],
});


const updateData = async (data: Series[]) => {
    const series: any = []
    const legendNames: any = []
    option.value.xAxis.data = new Array<number>()
    for (let i = 0; i < data.length; i++) {
        const value = data[i];
        series.push({
            name: value.name,
            type: value.type || 'line',
            symbol: 'none',
            data: new Array<number>(),
        })
        legendNames.push(value.name || '')
    }
    option.value.series = series
    option.value.legend = [{
        data: legendNames,
        bottom: '0%',
        textStyle: {
            fontSize: 12,
        },
    }]

    for (let i = 0; i < data.length; i++) {
        const value = data[i];
        for (let j = 0; j < value.data.length; j++) {
            const point = value.data[j]

            if (i == 0) {
                option.value.xAxis.data.push(point.timestamp || 0);
            }
            option.value.series[i].data.push(point.value);
        }

    }
    chart?.setOption(option.value);
}

const refreshChart = () => {
  chart?.setOption(option.value);
}

let chart: echarts.ECharts
// 待组件挂载完成后初始化 ECharts
onMounted(() => {
    const isDarkMode = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const theme = isDarkMode ? 'dark' : 'light';

    chart = echarts.init(chartElement.value,theme);
    chart.setOption(option.value);

    // startRealtimeData();
    updateData(props.data)
    refreshChart()
    window.addEventListener('resize', () => chart.resize())

})

watchEffect(() => {
    updateData(props.data)
    refreshChart()
});


</script>
<template>
    <div class="h-full w-full" ref="chartElement"></div>
</template>