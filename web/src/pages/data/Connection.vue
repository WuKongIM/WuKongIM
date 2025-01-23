<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue';
const loading = ref<boolean>(false);
import API from '../../services/API';
import { formatMemory } from '../../services/Utils';

const nodeTotal = ref<any>({}); // 节点列表
const selectedNodeId = ref<number>(1) // 选中的节点ID
const connectionTotal = ref<any>({}); // 连接列表
const sortField = ref<string>('id') // 排序字段

const uidSearch = ref<string>('') // 用户UID搜索

let connIntervalId: number;

onMounted(() => {

   API.shared.simpleNodes().then((res) => {
      nodeTotal.value = res
      if (nodeTotal.value.data.length > 0) {
         selectedNodeId.value = nodeTotal.value.data[0].id
      }
      loadConnections()
      startRequestConns()
   }).catch((err) => {
      alert(err)
   })
})

onBeforeUnmount(() => {
   window.clearInterval(connIntervalId)
})

const onNodeChange = (e: any) => {
   selectedNodeId.value = parseInt(e.target.value)
   loadConnections(e.target.value)
}

const loadConnections = (closeLoading?: boolean) => {
   if (!closeLoading) {
      loading.value = true;

   }
   API.shared.connections(selectedNodeId.value, 100, sortField.value, uidSearch.value).then((res) => {
      connectionTotal.value = res
   }).catch((err) => {
      alert(err)
   }).finally(() => {
      if (!closeLoading) {
         loading.value = false;
      }
   })
}

const startRequestConns = async () => {
   connIntervalId = window.setInterval(async () => {
      loadConnections(true)
   }, 1000)
}

const onSort = (s: string) => {
   if (sortField.value == s) {
      if (s.endsWith("Desc")) {
         sortField.value = s.substring(0, s.length - 4)
      } else {
         sortField.value = s + "Desc"
      }
   } else {
      sortField.value = s
   }
   loadConnections()
}

const onUidSearch = (e: any) => {
   uidSearch.value = e.target.value
   loadConnections()
}

// 断开连接
const onDisconnect = (conn: any) => {
   API.shared.disconnect({ uid: conn.uid, nodeId: conn.node_id, connId: conn.id,opNodeId:selectedNodeId.value }).then(() => {
      loadConnections()
   }).catch((err) => {
      alert(err)
   })
}

// 踢掉连接
const onKick = (conn: any) => {
   API.shared.kick({ uid: conn.uid, nodeId: conn.node_id, connId: conn.id }).then(() => {
      loadConnections()
   }).catch((err) => {
      alert(err)
   })
}

</script>


<template>
   <div>
      <div class="overflow-x-auto h-5/6">
         <div class="flex">
            <div class="text-sm ml-3">
               <label>节点</label>
               <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onNodeChange">
                  <option v-for="node in nodeTotal.data" :selected="node.id == selectedNodeId">{{ node.id }}</option>
               </select>
            </div>

            <div class="text-sm ml-10">
               <label>用户UID</label>
               <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                  v-on:change="onUidSearch" />
            </div>
         </div>
         <table class="table mt-10 table-pin-rows">
            <!-- head -->
            <thead>
               <tr>
                  <th>
                     <div class="flex items-center">
                        连接ID
                        <a href="#" v-on:click="() => onSort('id')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        源节点
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        用户UID
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        发出消息数
                        <a href="#" v-on:click="() => onSort('inMsg')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        收到消息数
                        <a href="#" v-on:click="() => onSort('outMsg')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        发出消息字节数
                        <a href="#" v-on:click="() => onSort('inMsgBytes')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        收到消息字节数
                        <a href="#" v-on:click="() => onSort('outMsgBytes')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>

                  </th>
                  <th>
                     <div class="flex items-center">
                        发出报文数
                        <a href="#" v-on:click="() => onSort('inPacket')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        收到报文数
                        <a href="#" v-on:click="() => onSort('outPacket')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        发出报文字节数
                        <a href="#" v-on:click="() => onSort('inPacketBytes')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        收到报文字节数
                        <a href="#" v-on:click="() => onSort('outPacketBytes')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        连接地址
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        存活时间
                        <a href="#" v-on:click="() => onSort('uptime')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>
                     <div class="flex items-center">
                        空闲时间
                        <a href="#" v-on:click="() => onSort('idle')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>

                  </th>
                  <th>
                     <div class="flex items-center">
                        协议版本
                        <a href="#" v-on:click="() => onSort('protoVersion')">
                           <svg class="w-3 h-3 ms-1.5" aria-hidden="true" xmlns="http://www.w3.org/2000/svg"
                              fill="currentColor" viewBox="0 0 24 24">
                              <path
                                 d="M8.574 11.024h6.852a2.075 2.075 0 0 0 1.847-1.086 1.9 1.9 0 0 0-.11-1.986L13.736 2.9a2.122 2.122 0 0 0-3.472 0L6.837 7.952a1.9 1.9 0 0 0-.11 1.986 2.074 2.074 0 0 0 1.847 1.086Zm6.852 1.952H8.574a2.072 2.072 0 0 0-1.847 1.087 1.9 1.9 0 0 0 .11 1.985l3.426 5.05a2.123 2.123 0 0 0 3.472 0l3.427-5.05a1.9 1.9 0 0 0 .11-1.985 2.074 2.074 0 0 0-1.846-1.087Z" />
                           </svg>
                        </a>
                     </div>
                  </th>
                  <th>设备</th>
                  <th>设备编号</th>
                  <th>代理类型</th>
                  <th>领导节点</th>
                  <th>操作</th>
               </tr>
            </thead>
            <tbody>
               <!-- row 1 -->
               <tr v-for="conn in connectionTotal.connections">
                  <td class="text-blue-800">{{ conn.id }}</td>
                  <td>{{ conn.node_id }}</td>
                  <td>{{ conn.uid }}</td>
                  <td>{{ conn.in_msgs }}</td>
                  <td>{{ conn.out_msgs }}</td>
                  <td>{{ formatMemory(conn.in_msg_bytes) }}</td>
                  <td>{{ formatMemory(conn.out_msg_bytes) }}</td>
                  <td>{{ conn.in_packets }}</td>
                  <td>{{ conn.out_packets }}</td>
                  <td>{{ formatMemory(conn.in_packet_bytes) }}</td>
                  <td>{{ formatMemory(conn.out_packet_bytes) }}</td>
                  <td>{{ `${conn.ip}:${conn.port}` }}</td>
                  <td>{{ conn.uptime }}</td>
                  <td>{{ conn.idle }}</td>
                  <td>{{ conn.version }}</td>
                  <td>{{ conn.device }}</td>
                  <td>{{ conn.device_id }}</td>
                  <td>{{ conn.proxy_type_format }}</td>
                  <td>{{ conn.leader_id }}</td>
                  <td class="flex flex-wrap gap-2">
                     <button class="btn btn-sm" v-on:click="() => onDisconnect(conn)">断开</button>
                     <button class="btn btn-sm" v-on:click="() => onKick(conn)">踢掉</button>
                  </td>
               </tr>

            </tbody>
         </table>
         <div class="flex flex-col gap-4 w-full mt-2" v-if="loading">
            <div class="skeleton h-6 w-full"></div>
            <div class="skeleton h-6 w-full"></div>
            <div class="skeleton h-6 w-full"></div>
            <div class="skeleton h-6 w-full"></div>
            <div class="skeleton h-6 w-full"></div>
            <div class="skeleton h-6 w-full"></div>
         </div>
      </div>

      <div class="flex justify-end mt-10 mr-10">
         <div class="flex pr-4">
            <div class="flex items-center">
               <ul class="text-sm">
                  <li>
                     总数量: <span class="text-blue-800">{{ connectionTotal.total }}</span>
                  </li>
               </ul>
            </div>
         </div>
      </div>
   </div>
</template>