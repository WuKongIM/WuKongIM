<script lang="ts" setup>
import { defineComponent, onMounted, ref } from 'vue';
import VueJsonPretty from 'vue-json-pretty';
import 'vue-json-pretty/lib/styles.css';
import API from '../../services/API';

const config = ref<any>({});

defineComponent({
  components: {
    VueJsonPretty,
  },
});

onMounted(() => {
  console.log('mounted');
  API.shared.clusterConfig().then((res) => {
    config.value = res
  });
});
 

</script>

<template>
    <vue-json-pretty :data="config" class="overflow-auto"/>
</template>