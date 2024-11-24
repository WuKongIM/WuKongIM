<script setup lang="ts">
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';

// 常量
import { ActionWrite, CHANNEL_TYPE } from '@/constants';

import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';
import { ElMessage, ElMessageBox } from 'element-plus';
import Migrate from './components/Migrate.vue';
import Replicas from './components/Replicas.vue';
import ClusterConfig from './components/ClusterConfig.vue';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';
import { useRouter } from 'vue-router';
import { useUserStore } from '@/stores/modules/user.ts';

const router = useRouter();
const userStore = useUserStore();
/**
 * 查询条件
 * */
const nodeList = ref<any[]>([]);
const formOptions = reactive<VxeFormProps<any>>({
  data: {
    nodeId: null,
    channel_type: null,
    channel_id: null,
    offset_created_at: null,
    limit: 20,
    pre: 0
  },
  items: [
    {
      field: 'nodeId',
      title: '节点',
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择',
          style: {
            width: '180px'
          }
        },
        options: nodeList
      }
    },
    {
      field: 'channel_type',
      title: '频道类型',
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择频道类型',
          style: { width: '180px' }
        },
        options: CHANNEL_TYPE
      }
    },
    {
      field: 'channel_id',
      title: '频道ID',
      itemRender: { name: 'ElInput', props: { placeholder: '请输入频道ID' } }
    },
    {
      align: 'center',
      slots: { default: 'action' }
    }
  ]
});

const getNodes = async () => {
  const nodes: any[] = [];
  const res = await clusterApi.simpleNodes();
  if (res.data.length > 0) {
    res.data.map((item: any) => {
      nodes.push({
        value: item.id,
        label: item.id
      });
    });
  }
  nodeList.value = nodes;
  if (nodes.length > 0) {
    formOptions.data = {
      ...formOptions.data,
      nodeId: nodes[0].value
    };
    tableRef.value?.commitProxy('query');
  }
};
/** 搜索事件 **/
const formEvents: VxeFormListeners<any> = {
  /** 查询 **/
  submit() {
    tableRef.value?.commitProxy('query');
  },
  /** 重置 **/
  reset() {
    formOptions.data = {
      ...formOptions.data
    };
    if (nodeList.value.length > 0) {
      formOptions.data = {
        ...formOptions.data,
        nodeId: nodeList.value[0].value
      };
    }

    tableRef.value?.commitProxy('query');
  }
};

/**
 * 表格
 **/
const tableRef = ref<VxeGridInstance<any>>();
const currentPage = ref(1); // 当前页
const hasPrev = ref<boolean>(false); // 是否有上一页
const hasNext = ref<boolean>(true); // 是否有下一页

const loadList = async (query: any) => {
  const res = await clusterApi.nodeChannelConfigs({ ...query });
  if (res.data) {
    hasNext.value = res.more !== 1;
    hasPrev.value = currentPage.value <= 1;
    return res.data;
  }
  return [];
};

const gridOptions = reactive<VxeGridProps<any>>({
  showOverflow: true,
  height: 'auto',
  border: true,
  stripe: true,
  rowConfig: {
    isCurrent: true,
    isHover: true
  },
  scrollY: {
    enabled: true,
    gt: 0
  },
  toolbarConfig: {
    slots: {
      buttons: 'tools'
    },
    refresh: {
      icon: 'vxe-icon-refresh',
      iconLoading: 'vxe-icon-refresh roll'
    },
    zoom: {
      iconIn: 'vxe-icon-fullscreen',
      iconOut: 'vxe-icon-minimize'
    },
    custom: true
  },
  proxyConfig: {
    autoLoad: false,
    ajax: {
      query: () => {
        return loadList(formOptions.data);
      }
    }
  },
  columns: [
    { field: 'channel_id', title: '频道ID', minWidth: 220, fixed: 'left' },
    { field: 'channel_type_format', title: '频道类型', minWidth: 120 },
    { field: 'slot_id', title: '所属槽', minWidth: 120 },
    { field: 'slot_leader_id', title: '槽领导', minWidth: 120 },
    { field: 'leader_id', title: '频道领导', minWidth: 120 },
    { field: 'term', title: '任期', minWidth: 120 },
    { field: 'replicas', title: '副本节点', minWidth: 160 },
    { field: 'last_message_seq', title: '消息高度', minWidth: 120 },
    { field: 'last_append_time', title: '最后消息时间', minWidth: 120 },
    { field: 'active_format', title: '是否运行', minWidth: 100 },
    { field: 'status_format', title: '状态', minWidth: 100 },
    { field: 'action', title: '操作', width: 224, fixed: 'right', slots: { default: 'action' } }
  ]
});

/**
 * 分页切换
 */
const onPage = (type: 0 | 1) => {
  // 下一页
  if (type === 0) {
    currentPage.value = currentPage.value + 1;
  }

  // 上一页
  if (type === 1 && currentPage.value > 1) {
    currentPage.value = currentPage.value - 1;
  }

  formOptions.data = {
    ...formOptions.data,
    pre: type
  };

  tableRef.value?.commitProxy('query');
};

/**
 * 操作开始或者停止
 */
const onStartOrStop = (row: any) => {
  if (!userStore.hasPermission('channelStart', ActionWrite)) {
    return ElMessage({
      message: '没有操作权限！',
      type: 'warning',
      plain: true
    });
  }

  ElMessageBox({
    title: '系统提示',
    message: `是否确认${row.active === 1 ? '停止' : '开始'}，频道ID为【${row.channel_id}】的数据项？`,
    type: 'warning',
    showCancelButton: true,
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    beforeClose: (action, instance, done) => {
      if (action === 'confirm') {
        instance.confirmButtonLoading = true;
        // 停止
        if (row.active === 1) {
          clusterApi
            .channelStop({
              channelId: row.channel_id,
              channelType: row.channel_type,
              nodeId: row.leader_id
            })
            .then(() => {
              done();
            })
            .catch(() => {
              instance.confirmButtonLoading = false;
            });
        }
        // 开始
        if (row.active === 0) {
          clusterApi
            .channelStart({
              channelId: row.channel_id,
              channelType: row.channel_type,
              nodeId: row.leader_id
            })
            .then(() => {
              done();
            })
            .catch(() => {
              instance.confirmButtonLoading = false;
            });
        }
      } else {
        done();
      }
    }
  })
    .then(() => {
      ElMessage({
        message: '操作成功！',
        type: 'success',
        plain: true
      });
      tableRef.value?.commitProxy('query');
    })
    .catch(() => {
      console.log('点击取消');
    });
};

/**
 * 迁移
 */
const modelMigrate = ref(false);
const channelIdMigrate = ref('');
const channelTypeMigrate = ref(0);
const replicasMigrate = ref<number[]>([]);
const onMigrate = (row: any) => {
  if (!userStore.hasPermission('channelMigrate', ActionWrite)) {
    return ElMessage({
      message: '没有操作权限！',
      type: 'warning',
      plain: true
    });
  }
  channelIdMigrate.value = row.channel_id;
  channelTypeMigrate.value = row.channel_type;
  replicasMigrate.value = row.replicas;
  modelMigrate.value = true;
};

const onSubmit = () => {
  tableRef.value?.commitProxy('query');
};

/**
 * 配置
 */
const modelClusterConfig = ref(false);
const channelIdClusterConfig = ref('');
const channelTypeClusterConfig = ref(0);
const nodeIdClusterConfig = ref(0);
const onClusterConfig = (row: any) => {
  channelIdClusterConfig.value = row.channel_id;
  channelTypeClusterConfig.value = row.channel_type;
  nodeIdClusterConfig.value = row.leader_id;
  modelClusterConfig.value = true;
};

/**
 * 副本
 */
const modelReplicas = ref(false);
const channelIdReplicas = ref('');
const channelTypeReplicas = ref(0);

const onReplicas = (row: any) => {
  channelIdReplicas.value = row.channel_id;
  channelTypeReplicas.value = row.channel_type;
  modelReplicas.value = true;
};

/**
 * 消息
 * @param row
 */
const onMessage = (row: any) => {
  router.push({
    path: '/data/message',
    query: { channel_id: row.channel_id, channel_type: row.channel_type }
  });
};

onMounted(() => {
  getNodes();
});
</script>

<template>
  <wk-page class="flex-col">
    <!-- S 查询条件 -->
    <div class="mb-12px pt-4px pb-4px card">
      <vxe-form v-bind="formOptions" v-on="formEvents">
        <template #action>
          <el-button native-type="submit" type="primary">查询</el-button>
          <el-button native-type="reset">重置</el-button>
        </template>
      </vxe-form>
    </div>
    <!-- E 查询条件 -->

    <!-- S 表格 -->
    <div class="flex-1 card !pt-4px overflow-hidden">
      <vxe-grid ref="tableRef" v-bind="gridOptions">
        <template #tools>
          <el-space>
            <el-button type="primary" :disabled="hasPrev" @click="onPage(1)">上一页</el-button>
            <el-button type="primary" :disabled="hasNext" @click="onPage(0)">下一页</el-button>
          </el-space>
        </template>

        <template #action="{ row }">
          <el-space>
            <el-button :type="row.active === 1 ? 'danger' : 'primary'" link @click="onStartOrStop(row)">
              {{ row.active === 1 ? '停止' : '开始' }}
            </el-button>
            <el-button type="primary" link @click="onMigrate(row)">迁移</el-button>
            <el-button type="primary" link @click="onClusterConfig(row)">配置</el-button>
            <el-button type="primary" link @click="onReplicas(row)">副本</el-button>
            <el-button type="primary" link @click="onMessage(row)">消息</el-button>
          </el-space>
        </template>
      </vxe-grid>
    </div>
    <!-- E 表格 -->

    <!-- 迁移 -->
    <Migrate
      v-model="modelMigrate"
      :channel-id="channelIdMigrate"
      :channel-type="channelTypeMigrate"
      :replicas="replicasMigrate"
      @submit="onSubmit"
    />

    <!-- 配置 -->
    <ClusterConfig
      v-model="modelClusterConfig"
      :channel-id="channelIdClusterConfig"
      :channel-type="channelTypeClusterConfig"
      :node-id="nodeIdClusterConfig"
    />

    <!-- 副本 -->
    <Replicas v-model="modelReplicas" :channel-id="channelIdReplicas" :channel-type="channelTypeReplicas" />
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 频道
</route>
