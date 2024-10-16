<template>
    <div id="container" class="w-full h-full"></div>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Graph } from '@antv/x6'
import { register } from '@antv/x6-vue-shape'
import SpanNode from '../../components/SpanNode.vue'
import API from "../../services/API";

const clientMsgNo = ref('')
const messageId = ref(0)



const nodeWidth = 180
const nodeHeight = 70

register({
    shape: 'spanNode',
    width: nodeWidth,
    height: nodeHeight,
    component: SpanNode,
})

Graph.registerEdge("custom-edge-label", {
    inherit: 'edge',
    defaultLabel: {
        markup: [
            {
                tagName: 'rect',
                selector: 'body',
            },
            {
                tagName: 'text',
                selector: 'label',
            },
        ],
        attrs: {
            label: {
                fill: '#000',
                fontSize: 14,
                textAnchor: 'middle',
                textVerticalAnchor: 'middle',
                pointerEvents: 'none',
            },
            body: {
                ref: 'label',
                fill: '#ffd591',
                stroke: '#ffa940',
                strokeWidth: 2,
                rx: 4,
                ry: 4,
                refWidth: '140%',
                refHeight: '140%',
                refX: '-20%',
                refY: '-20%',
            },
        },
        position: 0.5,
    },
}, true)



onMounted( async () => {
    const container = document.getElementById('container')
    const containerWidth = container!.offsetWidth
    const containerHeight = container!.offsetHeight


    const result = await requestMessageTrace({
        width: containerWidth,
        height: containerHeight
    })


    const nodes = []
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
                    description: node.description
                }
            })
        }
    }

    // const data = {
    //     nodes: [
    //         {
    //             id: 'recvMsg',
    //             shape: 'spanNode',
    //             x: centerX,
    //             y: topSpace,
    //             data: {
    //                 name: '收到消息',
    //                 time: '2024-10-11',
    //                 icon: '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-circle-play"><circle cx="12" cy="12" r="10"/><polygon points="10 8 16 12 10 16 10 8"/></svg>'
    //             },
    //         },
    //         {
    //             id: 'processPayloadDecrypt',
    //             shape: 'spanNode',
    //             x: centerX,
    //             y: nodeHeight + topSpace * 2,
    //             data: {
    //                 name: '解密消息',
    //                 time: '2024-10-11',
    //                 icon: '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-shield"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/></svg>'
    //             },
    //         },
    //         {
    //             id: 'processForward',
    //             shape: 'spanNode',
    //             x: leftSpace,
    //             y: nodeHeight + topSpace * 2,
    //             data: {
    //                 name: '转至节点',
    //                 time: '2024-10-11',
    //             },
    //         },
    //         {
    //             id: 'processPermission',
    //             shape: 'spanNode',
    //             x: centerX,
    //             y: nodeHeight * 2 + topSpace * 3,
    //             data: {
    //                 name: '权限判断',
    //                 time: '2024-10-11',
    //                 icon: '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-person-standing"><circle cx="12" cy="5" r="1"/><path d="m9 20 3-6 3 6"/><path d="m6 8 6 2 6-2"/><path d="M12 10v4"/></svg>'
    //             },
    //         },
    //         {
    //             id: 'storeMessages',
    //             shape: 'spanNode',
    //             x: centerX,
    //             y: nodeHeight * 3 + topSpace * 4,
    //             data: {
    //                 name: '存储消息',
    //                 time: '2024-10-11',
    //                 icon: '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-cylinder"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/></svg>'
    //             },
    //         },
    //         {
    //             id: 'sendackFailed',
    //             shape: 'spanNode',
    //             x: containerWidth - nodeWidth - topSpace,
    //             y: nodeHeight * 2 + topSpace * 3,
    //             data: {
    //                 name: '回应客户端失败',
    //                 time: '2024-10-11',
    //             },
    //         },
    //         {
    //             id: 'sendackSuccess',
    //             shape: 'spanNode',
    //             x: containerWidth - nodeWidth - topSpace,
    //             y: nodeHeight * 3 + topSpace * 4,
    //             data: {
    //                 name: '回应客户端成功',
    //                 time: '2024-10-11',
    //             },
    //         },
    //         // {
    //         //     id: 'webhook',
    //         //     shape: 'spanNode',
    //         //     x: 160,
    //         //     y: 180,
    //         //     data: {
    //         //         name: 'webhook',
    //         //         time: '2024-10-11',
    //         //     },
    //         // },
    //         {
    //             id: 'deliverMessage',
    //             shape: 'spanNode',
    //             x: centerX,
    //             y: nodeHeight * 4 + topSpace * 5,
    //             data: {
    //                 name: '投递消息',
    //                 time: '2024-10-11',
    //                 icon: '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide lucide-package-2"><path d="M3 9h18v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V9Z"/><path d="m3 9 2.45-4.9A2 2 0 0 1 7.24 3h9.52a2 2 0 0 1 1.8 1.1L21 9"/><path d="M12 3v6"/></svg>'
    //             },
    //         },
    //         {
    //             id: 'offlineUsers',
    //             shape: 'spanNode',
    //             x: 160,
    //             y: 180,
    //             data: {
    //                 name: '离线人数(1239)',
    //                 time: '2024-10-11',
    //             },
    //         },
    //         {
    //             id: 'onlineUsers',
    //             shape: 'spanNode',
    //             x: 160,
    //             y: 180,
    //             data: {
    //                 name: '在线人数(180)',
    //                 time: '2024-10-11',
    //             },
    //         },
    //         {
    //             id: 'deliverNode1',
    //             shape: 'spanNode',
    //             x: 160,
    //             y: nodeHeight * 5 + topSpace * 6,
    //             width: 140,
    //             height: 70,
    //             data: {
    //                 name: '投至节点1',
    //                 time: '2024-10-11',
    //             },
    //         },
    //         {
    //             id: 'deliverNode2',
    //             shape: 'spanNode',
    //             x: 160,
    //             y: nodeHeight * 5 + topSpace * 6,
    //             width: 140,
    //             height: 70,
    //             data: {
    //                 name: '投至节点2',
    //                 time: '2024-10-11',
    //             },
    //         },
    //         {
    //             id: 'deliverNode3',
    //             shape: 'spanNode',
    //             x: 160,
    //             y: nodeHeight * 5 + topSpace * 6,
    //             width: 140,
    //             height: 70,
    //             data: {
    //                 name: '投至节点3',
    //                 time: '2024-10-11',
    //             },
    //         },

    //     ],
    //     edges: [
    //         {
    //             source: 'recvMsg',
    //             target: 'processPayloadDecrypt',
    //         },
    //         {
    //             source: 'processPayloadDecrypt',
    //             target: 'processForward',
    //             shape: 'custom-edge-label',
    //             labels: [
    //                 {
    //                     attrs: {
    //                         line: {
    //                             stroke: '#73d13d',
    //                         },
    //                         text: {
    //                             text: '成功',
    //                         },
    //                     },
    //                 },
    //             ],
    //         },
    //         {
    //             source: 'processForward',
    //             target: 'processPermission',
    //             shape: 'custom-edge-label',
    //             labels: [
    //                 {
    //                     attrs: {
    //                         line: {
    //                             stroke: '#73d13d',
    //                         },
    //                         text: {
    //                             text: '成功',
    //                         },
    //                     },
    //                 },
    //             ],
    //         },
    //         {
    //             source: 'processPayloadDecrypt',
    //             target: 'sendackFailed',


    //             shape: 'custom-edge-label',
    //             labels: [
    //                 {
    //                     attrs: {
    //                         line: {
    //                             stroke: '#73d13d',
    //                         },
    //                         text: {
    //                             text: '解码失败',
    //                         },
    //                     },
    //                 },
    //             ],
    //         },

    //         {
    //             source: 'processPayloadDecrypt',
    //             target: 'processPermission',
    //         },
    //         {
    //             source: 'processPermission',
    //             target: 'storeMessages',
    //         },
    //         {
    //             source: 'processPermission',
    //             target: 'sendackFailed',

    //             shape: 'custom-edge-label',
    //             labels: [
    //                 {
    //                     attrs: {
    //                         line: {
    //                             stroke: '#73d13d',
    //                         },
    //                         text: {
    //                             text: '权限失败',
    //                         },
    //                     },
    //                 },
    //             ],
    //         },
    //         {
    //             source: 'storeMessages',
    //             target: 'deliverMessage',
    //         },
    //         {
    //             source: 'storeMessages',
    //             target: 'sendackFailed',

    //             shape: 'custom-edge-label',
    //             labels: [
    //                 {
    //                     attrs: {
    //                         line: {
    //                             stroke: '#73d13d',
    //                         },
    //                         text: {
    //                             text: '存储失败',
    //                         },
    //                     },
    //                 },
    //             ],
    //         },
    //         {
    //             source: 'storeMessages',
    //             target: 'sendackSuccess',

    //             shape: 'custom-edge-label',
    //             labels: [
    //                 {
    //                     attrs: {
    //                         line: {
    //                             stroke: '#73d13d',
    //                         },
    //                         text: {
    //                             text: '存储成功',
    //                         },
    //                     },
    //                 },
    //             ],
    //         },


    //     ],
    // }


    const graph = new Graph({
        container: container!,
        width: containerWidth,
        height: containerHeight,
        panning: true,
        background: {
            color: '#F2F7FA',
        },
        grid: {
            visible: true,
            type: 'doubleMesh',
            args: [
                {
                    color: '#eee', // 主网格线颜色
                    thickness: 1, // 主网格线宽度
                },
                {
                    color: '#ddd', // 次网格线颜色
                    thickness: 1, // 次网格线宽度
                    factor: 4, // 主次网格线间隔
                },
            ],
        },
    })

    graph.addNodes(nodes)

    // graph.centerContent() // 居中显示

});


const requestMessageTrace = ({ 
    width,
    height
}:any) => {
   return  API.shared.messageTraces({
        clientMsgNo: clientMsgNo.value,
        messageId: messageId.value,
        width: width,
        height: height
    })
}
</script>