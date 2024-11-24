<script setup lang="ts">
import { onMounted, ref, nextTick } from 'vue';
import APIClient from '../../services/APIClient';
import App from '../../services/App';
import API from '../../services/API';

class LabelInput {
    name: string = ""
    operator: string = ""
    value: string = ""
}


const logs = ref(new Array<string>()) // 日志行
const labelInputs = ref(new Array<LabelInput>()) // 属性输入
const nodes = ref(new Array()) // 节点列表
const nodeId = ref("") // 节点ID
const logLevel = ref("") // 日志等级
const time = ref("1h") // 时间
const searchValue = ref("") // 搜索输入
const tail = ref(false) // 是否实时刷新
const ws = ref<WebSocket>()
const preUrl = ref("")

// 创建WebSocket实例

let wsUrl = ""

if (APIClient.shared.config.apiURL.startsWith('http')) {
    wsUrl = APIClient.shared.config.apiURL.replace('http', 'ws')
    wsUrl = `${wsUrl}/cluster/logs/tail`
} else {
    wsUrl = `ws://${window.location.host}/cluster/logs/tail`
}


onMounted(() => {

    API.shared.simpleNodes().then((res) => {
        nodes.value = res.data

    }).catch((err) => {
        alert(err)
    })

    search()
})

const search = () => {

    if (ws.value) {
        ws.value.close()
        ws.value = undefined
    }

    const u = new URL(wsUrl)
    u.searchParams.append("Authorization", `Bearer ${App.shard().loginInfo.token}`)

    u.searchParams.append("level", logLevel.value)
    u.searchParams.append("time", time.value)
    u.searchParams.append("search", searchValue.value)
    u.searchParams.append("tail", tail.value ? "1" : "0")
    u.searchParams.append("node_id", nodeId.value.toString())
    u.searchParams.append("labels", JSON.stringify(labelInputs.value.filter((labelInput) => {
        return labelInput.name.length > 0 && labelInput.value.length > 0 && labelInput.operator.length > 0
    }).map((labelInput) => {
        return {
            name: labelInput.name,
            operator: labelInput.operator,
            value: labelInput.value
        }
    })))

    const reqUrl = u.toString()
    if (reqUrl === preUrl.value) {
        return
    }
    preUrl.value = reqUrl

    let loading = true // 加载日志中


    logs.value = new Array<string>() // 清空日志

    ws.value = new WebSocket(reqUrl)
    ws.value.onopen = function () {
        console.log('WebSocket is open now.');
    };

    ws.value.onmessage = function (event) {
        console.log('WebSocket message received:', event.data);

        if (loading) {
            const results = JSON.parse(event.data)
            if (!tail.value) {
                logs.value.push(...results.map((log: string) => log.replace(/\n/g, '')));
            }
            loading = false
            scrollToBottom()
        }
       
        if (tail.value) {
            const results = JSON.parse(event.data)
            logs.value.push(...results.map((log: string) => log.replace(/\n/g, '')));
            scrollToBottom()
        } 
    };

    ws.value.onclose = function (event) {
        console.log('WebSocket is closed now.', event);
    };
}


const scrollToBottom = () => {
    nextTick(() => {
        const el = document.querySelector("#logBox")
        if (el) {
            el.scrollTop = el.scrollHeight
        }
    })

}

const onAddLabelInput = () => {
    const labelInput = new LabelInput()
    labelInput.operator = "="
    labelInputs.value.push(labelInput)
}

const onRemoveLabelInput = (index: number) => {
    labelInputs.value.splice(index, 1)
    search()
}


</script>


<template>
    <div class="grid grid-rows-[auto,1fr,auto]">
        <div class="flex flex-wrap gap-4 mb-4">
            <div class="flex text-sm ml-4 items-center">
                <label>节点</label>
                <select class="select select-bordered select-sm ml-2 max-w-xs " v-model="nodeId" v-on:change="search">
                    <option selected value="">All</option>
                    <option v-for="node in nodes" :value="node.id">{{ node.id }}</option>
                </select>
            </div>
            <div class="flex text-sm ml-4 items-center">
                <label>日志等级</label>
                <select class="select select-bordered select-sm ml-2 max-w-xs " v-model="logLevel" v-on:change="search">
                    <option selected value="">All</option>
                    <option value="info">Info</option>
                    <option value="warn">Warn</option>
                    <option value="error">Error</option>
                    <option value="trace">Trace</option>
                </select>
            </div>

            <div class="flex items-center text-sm ml-4">
                <label>时间</label>
                <select class="select select-bordered select-sm ml-2 max-w-xs" v-model="time" v-on:change="search">
                    <option selected value="5m">5分钟</option>
                    <option value="30m">30分钟</option>
                    <option value="1h">1小时</option>
                    <option value="6h">6小时</option>
                    <option value="12h">12小时</option>
                    <option value="24h">24小时</option>
                </select>
            </div>

            <div class="flex items-center text-sm ml-4">
                <label>搜索</label>
                <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2" v-model="searchValue"
                    v-on:change="search" />
            </div>
            <div class="text-sm ml-4 flex items-center">
                <label>实时刷新</label>
                <input type="checkbox" class="checkbox ml-2" v-model="tail" />
            </div>
            <div class="text-sm ml-4 flex items-center">
                <label>属性</label>
                <div class="flex flex-wrap">
                    <div class="text-sm ml-2 border grid-rows-[auto,1fr] w-60 rounded"
                        v-for="(labelInput, index) in labelInputs">
                        <div class="flex justify-between border-b-[0.02rem] h-8 items-center">
                            <div class="ml-2">属性筛选</div>
                            <div class="cursor-pointer mr-2" v-on:click="() => onRemoveLabelInput(index)">
                                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                    fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                    stroke-linejoin="round" class="lucide lucide-x">
                                    <path d="M18 6 6 18" />
                                    <path d="m6 6 12 12" />
                                </svg>
                            </div>
                        </div>
                        <div class="grid grid-rows-3 p-2 space-y-1">
                            <div class="flex items-center">
                                <div class="w-20">属性名</div>
                                <input type="text" class="input input-bordered w-28 h-8 text-sm"
                                    v-model="labelInput.name" v-on:change="search" />
                            </div>
                            <div class="flex items-center">
                                <div class="w-20">操作</div>
                                <select class="select select-bordered select-sm w-28 h-8" v-model="labelInput.operator">
                                    <option selected value="=">等于</option>
                                    <option value="~~">包含</option>
                                    <option value="!=">不等于</option>
                                    <option value="!~">不包含</option>
                                </select>
                            </div>
                            <div class="flex items-center">
                                <div class="w-20">属性值</div>
                                <input type="text" class="input input-bordered w-28 h-8 text-sm"
                                    v-model="labelInput.value" v-on:change="search" />
                            </div>
                        </div>
                    </div>

                    <button class="btn btn-square btn-md ml-2" v-on:click="onAddLabelInput">添加</button>
                </div>

            </div>
        </div>
        <!-- <div class="mockup-code overflow-scroll text-sm w-full h-full">
            <pre v-for="log in logs">
                <code >{{ log }}</code>
            </pre>
        </div> -->
        <div class="overflow-hidden">
            <div class="coding inverse-toggle px-5 pt-4 shadow-lg text-gray-100 text-sm font-mono subpixel-antialiased 
              bg-gray-800  pb-6 pt-4 rounded-lg leading-normal overflow-hidden h-full">
                <div class="top mb-2 flex">
                    <div class="h-3 w-3 bg-red-500 rounded-full"></div>
                    <div class="ml-2 h-3 w-3 bg-orange-300 rounded-full"></div>
                    <div class="ml-2 h-3 w-3 bg-green-500 rounded-full"></div>
                </div>
                <div class="mt-4 overflow-scroll h-full" id="logBox">
                    <p class="text whitespace-nowrap leading-6" v-for="log in logs">
                        {{ log }}
                    </p>
                    <br /> <br />
                </div>
            </div>
        </div>
        <div>
            <br />
        </div>
    </div>

</template>
