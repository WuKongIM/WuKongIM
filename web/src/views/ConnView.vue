
<script setup lang="ts">
import APIClient from '@/services/APIClient';
import { Connz, newConnz } from '@/services/Model';
import { formatMemory } from '@/services/Utils';
import { onBeforeUnmount, onMounted, ref } from 'vue';

const connz = ref<Connz>(new Connz())

const sort = ref<string>("id")


let connIntervalId: number;
const requestConns = async () => {
    const connzObj = await APIClient.shared.get("/api/connz", {
        param: {
            "limit": 20,
            "offset": 0,
            "sort": sort.value
        }
    })
    connz.value = newConnz(connzObj)

}

const startRequestConns = async () => {
    connIntervalId = window.setInterval(async () => {
        requestConns()
    }, 1000)
}

onMounted(() => {
    requestConns()
    startRequestConns()
})

onBeforeUnmount(() => {
    window.clearInterval(connIntervalId)
})

const onSort = (s: string) => {
    if(sort.value == s) {
        if(s.endsWith("Desc")) {
            sort.value = s.substring(0, s.length - 4)
        }else{
            sort.value = s + "Desc"
        }
    }else{
        sort.value = s
    }
    requestConns()
}

</script>
<template>
    <div>
        <div class="join overflow-auto">
            <input class="join-item btn" type="radio" name="options" aria-label="ID" v-on:click="() => onSort('id')" />
            <input class="join-item btn" type="radio" name="options" aria-label="收到消息数"
                v-on:click="() => onSort('inMsgDesc')" />
            <input class="join-item btn" type="radio" name="options" aria-label="发出消息数"
                v-on:click="() => onSort('outMsgDesc')" />
            <input class="join-item btn" type="radio" name="options" aria-label="收到字节数"
                v-on:click="() => onSort('inBytesDesc')" />
            <input class="join-item btn" type="radio" name="options" aria-label="发出字节数"
                v-on:click="() => onSort('outBytesDesc')" />
                <input class="join-item btn" type="radio" name="options" aria-label="待发送字节"
                v-on:click="() => onSort('pendingBytesDesc')" />
            <input class="join-item btn" type="radio" name="options" aria-label="存活时间" v-on:click="() => onSort('uptime')" />
            <input class="join-item btn" type="radio" name="options" aria-label="空闲时间" v-on:click="() => onSort('idle')" />
        </div>
        <div class="pt-10">
            <div class="overflow-x-auto">
                <table class="table">
                    <thead>
                        <tr>
                            <th>ID</th>
                            <th>UID</th>
                            <th>收到消息数</th>
                            <th>发出消息数</th>
                            <th>收到字节数</th>
                            <th>发出字节数</th>
                            <th>待发送字节</th>
                            <th>连接地址</th>
                            <th>存活时间</th>
                            <th>空闲时间</th>
                            <th>协议</th>
                            <th>设备</th>
                            <th>设备等级</th>
                        </tr>
                    </thead>
                    <tbody>
                        <tr v-for="conn in connz.connections">
                            <td>{{ conn.id }}</td>
                            <td>{{ conn.uid }}</td>
                            <td>{{ formatMemory(conn.inMsgs) }}</td>
                            <td>{{ formatMemory(conn.outMsgs) }}</td>
                            <td>{{ formatMemory(conn.inBytes) }}</td>
                            <td>{{ formatMemory(conn.outBytes) }}</td>
                            <td>{{ formatMemory(conn.pendingBytes) }}</td>
                            <td>{{ conn.ip }}:{{ conn.port }}</td>
                            <td>{{ conn.uptime }}</td>
                            <td>{{ conn.idle }}</td>
                            <td>{{ conn.version }}</td>
                            <td>{{ conn.device }}</td>
                            <td>{{ conn.deviceID }}</td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
</template>

<style scoped>
.btn:is(input[type="radio"]:checked) {
    background-color: var(--theme-color);
    color: #fff;
    border-color: var(--theme-color);
}</style>