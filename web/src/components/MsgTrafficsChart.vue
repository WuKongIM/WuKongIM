<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watchEffect } from 'vue';
import * as echarts from "echarts";
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

const formatNumber = (num: number) => {
    if (num < 1000) {
        return num.toString();
    } else {
        const suffixes = ['', 'K', 'M', 'G', 'T', 'P', 'E', 'Z', 'Y'];
        let quotient = num;
        let suffixIndex = 0;
        while (quotient >= 1000 && suffixIndex < suffixes.length - 1) {
            quotient = Math.floor(quotient / 1000);
            suffixIndex++;
        }
        const suffix = suffixes[suffixIndex];
        const formattedQuotient = quotient.toString();
        if (suffix === '') {
            return formattedQuotient;
        } else {
            const formattedRemainder = Math.floor((num % 1000) / 100).toString();
            return `${formattedQuotient}.${formattedRemainder}${suffix}`;
        }
    }
}

const formatDate = (date: Date) => {
    const hours = date.getHours();
    const minutes = date.getMinutes();
    const seconds = date.getSeconds();
    return hours + ':' + minutes + ':' + seconds;
}

const option = ref({
    title: {
        text: '消息流量(byte/s)',
        textStyle: {
            fontWeight: 'normal',
            fontSize: 12
        }
    },
    legend: [
        {
            data: ['上行流量', '下行流量'],
            bottom: '0%',
            textStyle: {
                fontSize: 12,
            },
        },
    ],
    tooltip: {
        show: true,
        trigger: 'axis',
        formatter: function (params: any) {
            var res = formatDate(new Date(params[0].name*1000)) + '<br/>'
            for (var i = 0, length = params.length; i < length; i++) {
                res += params[i].marker + params[i].seriesName + '：'
                    + formatNumber(params[i].value) + '<br/>'
            }
            return res

        }
    },
    grid: {
        left: "1%",
        top: "15%",
        bottom: "15%",
        containLabel: true,
    },
    xAxis: {
        type: 'category',
        axisLabel: {
            formatter: function (value: number, index: number) {
                const date = new Date(value * 1000);
                return formatDate(date)
            },

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
            name: '上行流量',
            type: 'line',
            symbol: 'none',
            data: new Array<number>(),
        },
        {
            name: '下行流量',
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
let intervalId: number

// 待组件挂载完成后初始化 ECharts
onMounted(() => {


    chart = echarts.init(connChart.value);
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
    clearInterval(intervalId)
    if (chart) {
        chart.dispose()
    }
})

</script>


<template>
    <div class="h-full w-full" ref="connChart"></div>
</template>