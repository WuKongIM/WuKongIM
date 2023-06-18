
<script setup lang="ts">

import { Varz } from '../services/Model'
import { formatMemory,formatNumber } from '@/services/Utils';

const props = defineProps({
    varz: {
        type: Varz,
        required: true
    },
})


</script>

<template>
    <div class="h-30 w-full">
        <!-- header -->
        <div class="flex flex-wrap pb-10">
            <div class="felx flex-col p-4 pr-5 min-w-[9rem]">
                <div class="text-sm text-gray-500">运行时间</div>
                <div class="text-xl font-bold">{{ varz.uptime }}</div>
            </div>
            <div class="felx flex-col p-4 pr-5 min-w-[8rem]">
                <div class="text-sm text-gray-500">上行消息数</div>
                <div class="text-xl font-bold">{{ formatNumber(varz.inMsgs) }}</div>
            </div>
            <div class="felx flex-col p-4 pr-5 min-w-[8rem]">
                <div class="text-sm text-gray-500">上行流量</div>
                <div class="text-xl font-bold">{{ formatMemory(varz.inBytes) }}</div>
            </div>
            <div class="felx flex-col p-4 pr-5 min-w-[8rem]">
                <div class="text-sm text-gray-500">下行消息数</div>
                <div class="text-xl font-bold">{{formatNumber(varz.outMsgs)}}</div>
            </div>
            <div class="felx flex-col p-4 pr-5 min-w-[8rem]">
                <div class="text-sm text-gray-500">下行流量</div>
                <div class="text-xl font-bold">{{formatMemory(varz.outBytes)}}</div>
            </div>
            <div class="felx flex-col p-4 pr-5 min-w-[8rem]">
                <div class="text-sm text-gray-500">慢客户端</div>
                <div class="text-xl font-bold">{{formatNumber(varz.slowClients)}}</div>
            </div>
            <div class="felx flex-col p-4 pr-5 min-w-[8rem]">
                <div class="text-sm text-gray-500">应用占用内存</div>
                <div class="text-xl font-bold">{{formatMemory(varz.mem)}}</div>
            </div>
            <div class="felx flex-col p-4 min-w-[8rem]">
                <div class="text-sm text-gray-500">应用使用CPU</div>
                <div class="text-xl font-bold">{{varz.cpu}}%</div>
            </div>
        </div>
    </div>
</template>