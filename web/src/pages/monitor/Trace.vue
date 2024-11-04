<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Graph } from '@antv/x6'
import { register } from '@antv/x6-vue-shape'
import SpanNode from '../../components/SpanNode.vue'
import TextNode from '../../components/TextNode.vue'
import RecvackTable from '../../components/RecvackTable.vue'
import API from "../../services/API";
import { useRouter } from "vue-router";
import App from "../../services/App";

const clientMsgNo = ref('')
const messageId = ref(0)

const nodeWidth = 180
const nodeHeight = 70
const error = ref('')
const recvackResult = ref()

const router = useRouter()

const query = router.currentRoute.value.query; //查询参数

if (query.clientMsgNo) {
    clientMsgNo.value = query.clientMsgNo as string
}

const modalContent = ref('')

register({
    shape: 'spanNode',
    width: nodeWidth,
    height: nodeHeight,
    component: SpanNode,
})

register({
    shape: 'textNode',
    width: nodeWidth,
    height: nodeHeight,
    component: TextNode,
})



onMounted(async () => {
    App.shard().loadSystemSettingIfNeed()
    if (clientMsgNo.value && clientMsgNo.value.length > 0) {
        drawFlow()
    }
});


const drawFlow = async () => {

    const container = document.getElementById('container')
    const containerWidth = container!.offsetWidth
    const containerHeight = container!.offsetHeight

    error.value = ''
    const result = await requestMessageTrace({
        width: containerWidth,
        height: containerHeight
    }).catch((e) => {
        error.value = e.msg
    })


    const nodes = []
    const edges = []
    if (result.nodes) {
        for (let i = 0; i < result.nodes.length; i++) {
            const node = result.nodes[i]
            nodes.push({
                id: node.id,
                shape: node.shape,
                x: node.x,
                y: node.y,
                data: {
                    name: node.name,
                    time: node.time,
                    icon: node.icon,
                    duration: node.duration,
                    description: node.description,
                    data: node.data,
                }
            })
        }
    }

    if (result.edges) {
        for (let i = 0; i < result.edges.length; i++) {
            const edge = result.edges[i]

            const edgeObj = {
                source: edge.source,
                target: edge.target,
                connector: { name: 'smooth' },
                attrs: {
                    line: {
                        stroke: '#1890ff',
                        targetMarker: 'classic',
                        strokeDasharray: 0,
                    },
                },
            }

            if (edge.shape == "dashed") {
                edgeObj.attrs.line.strokeDasharray = 5
            }
            edges.push(edgeObj)
        }
    }


    const graph = new Graph({
        container: container!,
        width: containerWidth,
        height: containerHeight,
        panning: true,
        background: {
            color: '#F2F7FA',
        },
        grid: true,
        mousewheel: true,
    })

    graph.addNodes(nodes)
    graph.addEdges(edges)

    graph.on('node:click', async ({ node }) => {
        const nodeId = node.id as string
        if (nodeId === "processMessage") {
            const data = node.data.data
            modalContent.value = `
                                发送者: ${data.uid} <br> 
                                发送设备: ${data.deviceId} <br> 
                                设备类型: ${data.deviceFlag} <br> 
                                设备等级: ${data.deviceLevel} <br>
                                接受频道: ${data.channelId} <br>
                                频道类型: ${data.channelType} <br>
                                `
        }else if (nodeId.startsWith("deliverOnline") || nodeId.startsWith("deliverOffline")) {
            const data = node.data.data
            var uids: string[] = []
            if( data.uids) {
                data.uids.split(',').forEach((uid: string) => {
                    uids.push(uid)
                })
            }
            modalContent.value = '<div class="flex flex-wrap space-x-2">'
            for (let i = 0; i < uids.length; i++) {
                modalContent.value += `
                <a href="#" class="inline-block"> ${uids[i]}</a>
                `
            }
             modalContent.value += '</div>'
        } else if (nodeId.startsWith('processRecvack')) {
            const data = node.data.data
            const nodeId = data.nodeId
            const messageId = data.messageId
            recvackResult.value = await requestMessageRecvackTraces({
                nodeId: nodeId,
                messageId: messageId,
            })
            if(recvackResult.value) {
                const dialog = document.getElementById('recvackTable') as HTMLDialogElement;
                dialog.showModal();
            }
            return
        } else {
            return
        }
        const dialog = document.getElementById('content') as HTMLDialogElement;
        dialog.showModal();
    })

}


const requestMessageTrace = ({
    width,
    height,
}: any) => {
    return API.shared.messageTraces({
        clientMsgNo: clientMsgNo.value,
        messageId: messageId.value,
        width: width,
        height: height,
        since: 60 * 60 * 24,
    })
}

const requestMessageRecvackTraces = ({
    nodeId,
    messageId,
}:any) => {
    return API.shared.messageRecvackTraces({
        nodeId: nodeId,
        messageId: messageId,
        since: 60 * 60 * 24,
    })
}

const onSearch = (e: any) => {
    if (!App.shard().systemSetting.messageTraceOn) {
        error.value = '消息追踪功能未开启,请查看官网文档 https://githubim.com'
        return
    }

    clientMsgNo.value = e.target.value
    drawFlow()
}



</script>


<template>
    <div class="overflow-x-auto h-5/6">
        <div class="flex flex-wrap gap-4">
            <div class="text-sm ml-4">
                <label>消息编号</label>
                <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2" v-model="clientMsgNo"
                    v-on:change="onSearch" />
            </div>
            <div class="text text-red-500">{{ error }}</div>
        </div>
        <br />
        <div id="container" class="w-full h-full"></div>

        <dialog id="content" class="modal">
            <div class="modal-box flex flex-wrap gap-2">
                <div v-html="modalContent" class="break-words"></div>

            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

        <dialog id="recvackTable" class="modal">
            <div class="modal-box">
                <div class="min-w-[40rem]">
                    <RecvackTable :data="recvackResult" />
                </div>

            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>
    </div>
</template>