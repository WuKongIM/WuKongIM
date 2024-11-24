<script name="MigrateSlot" lang="ts" setup>
// API 接口
import { clusterApi } from '@/api/modules/cluster-api.ts';
import { ElMessage } from 'element-plus';

import type { VxeFormProps } from 'vxe-pc-ui';

interface IProps {
  slot: number;
  replicas: number[];
}

interface IReplicasItem {
  value: number;
  label: number;
}

/**
 * Props
 */
const props = withDefaults(defineProps<IProps>(), {
  slot: 0,
  replicas: []
});

const showModel = defineModel<boolean>();
const emits = defineEmits(['submit']);
const loadingModel = ref(false);
const replicasList = ref<IReplicasItem[]>([]);

watch(
  () => showModel.value,
  val => {
    if (val) {
      if (props.slot) {
        getNodes();

        const getReplicas: IReplicasItem[] = [];
        props.replicas.map(item => {
          getReplicas.push({
            value: item,
            label: item
          });
        });
        replicasList.value = getReplicas;

        formOptions.data = {
          ...formOptions.data,
          slot: props.slot
        } as FormDataVO;
      }
    } else {
      formOptions.data = {
        slot: 0,
        migrateFrom: null,
        migrateTo: null
      };
    }
  }
);

const nodeList = ref();
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
};

/**
 * 表单
 */

interface FormDataVO {
  slot: number;
  migrateFrom: number | null;
  migrateTo: number | null;
}

const formRef = ref();
const formOptions = reactive<VxeFormProps<FormDataVO>>({
  loading: false,
  data: {
    slot: 0,
    migrateFrom: null,
    migrateTo: null
  },
  rules: {
    migrateFrom: [{ required: true, message: '请选择副本节点' }],
    migrateTo: [{ required: true, message: '请选择迁移至节点' }]
  },
  titleBold: true,
  titleWidth: 100,
  titleColon: true,
  titleOverflow: true,
  items: [
    {
      field: 'migrateFrom',
      title: '副本节点',
      span: 24,
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择副本节点'
        },
        options: replicasList
      }
    },
    {
      field: 'migrateTo',
      title: '迁移至节点',
      span: 24,
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择迁移至节点'
        },
        options: nodeList
      }
    }
  ]
});

// 提交功能
const onFuncConfirm = async () => {
  const validate = await formRef.value.validate();
  if (validate) return;
  clusterApi.migrateSlot(formOptions.data as any).then(() => {
    ElMessage({
      message: '操作成功！',
      type: 'success',
      plain: true
    });
    emits('submit');
  });
};

onMounted(() => {
  getNodes();
});
</script>

<template>
  <vxe-modal
    v-model="showModel"
    title="迁移"
    :width="460"
    :height="360"
    :confirm-closable="false"
    :padding="false"
    :loading="loadingModel"
    show-maximize
    show-footer
    show-confirm-button
    show-cancel-button
    @confirm="onFuncConfirm"
  >
    <wk-page class="flex-col wk-page-bg">
      <!-- S 表单 -->
      <div class="flex-1 card overflow-hidden">
        <vxe-form ref="formRef" v-bind="formOptions"></vxe-form>
      </div>
      <!-- E 表单 -->
    </wk-page>
  </vxe-modal>
</template>

<style scoped lang="scss">
.wk-page-bg {
  background-color: var(--el-bg-color-page);
}
</style>
