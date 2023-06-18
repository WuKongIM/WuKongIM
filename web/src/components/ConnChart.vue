<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watchEffect } from 'vue';
import * as echarts from "echarts";

const props = defineProps({
    data: {
        type: Array<number>,
        required: true
    },
})

const connChart = ref<any>(null);

const option = ref({
    title: {
        text: '实时连接数',
        textStyle: {
            fontWeight: 'normal',
            fontSize: 12
        }
    },
    grid: {
        left: "1%",
        top: "15%",
        bottom: "1%",
        containLabel: true,
    },
    xAxis: {
        type: 'category',
        boundaryGap: false,
        axisLabel: {
            formatter: function (value: number, index: number) {
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

    },
    series: [
        {
            name: '连接数据',
            type: 'line',
            smooth: true,
            symbol: 'none',
            stack: 'a',
            data: new Array<number>(),
        },
    ],
});

const updateData = async (data: number[],last:boolean) => {
    
    let currentTimestamp = Math.floor(Date.now() / 1000);
    for (let i = 0; i < data.length; i++) {
        if (!last) {
            currentTimestamp -= 1
        }
        const value = data[i];
        if (option.value.xAxis.data.length > 60) {
            option.value.xAxis.data.shift();
            option.value.series[0].data.shift();
        }
        option.value.xAxis.data.push(currentTimestamp);
        option.value.series[0].data.push(value);
    }
    chart?.setOption(option.value);
}


let chart: echarts.ECharts

// 待组件挂载完成后初始化 ECharts
onMounted(() => {

    let currentTimestamp = Math.floor(Date.now() / 1000);
    for (let i = 0; i < props.data.length; i++) {
        const value = props.data[i];
        currentTimestamp =currentTimestamp -  1;
        if (option.value.xAxis.data.length > 60) {
            option.value.xAxis.data.shift();
            option.value.series[0].data.shift();
        }
        option.value.xAxis.data.push(currentTimestamp);
        option.value.series[0].data.push(value);
    }

    chart = echarts.init(connChart.value);
    chart.setOption(option.value);

    // startRealtimeData();
    updateData(props.data,false)

    window.addEventListener('resize', () => chart.resize())

})

watchEffect(() => {
     updateData(props.data,props.data.length==1)
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