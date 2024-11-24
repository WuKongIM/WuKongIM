<template>
    <div class="w-full h-full">
        <table class="table table-pin-rows">
        <!-- head -->
        <thead>
            <tr>
                <th>
                    <div class="flex items-center">
                        连接ID
                    </div>
                </th>
                <th>
                    <div class="flex items-center">
                        用户UID
                    </div>
                </th>
                <th>
                    <div class="flex items-center">
                        设备ID
                    </div>
                </th>

                <th>
                    <div class="flex items-center">
                        收到时间
                    </div>
                </th>
            </tr>
        </thead>
        <tbody>

            <tr v-for="item in props.data?.data">
                <td>{{ item.conn_id }}</td>
                <td>{{ item.uid }}</td>
                <td>{{ item.device_id }}</td>
                <td>{{ item.time }}</td>
            </tr>

        </tbody>
    </table>
    </div>
</template>

<script setup lang="ts">

const props = defineProps({
    data: {
        type: Object,
        required: true
    },
})

</script>