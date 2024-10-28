<template>
     <div class="w-full h-full border border-blue-500 rounded-md shadow-md bg-white">
        <div class="w-full h-full font-semibold flex items-center text-[0.9rem] justify-center">
            <div>{{data.name}}</div>
        </div>
    </div>
</template>

<script setup lang="ts">
import { inject, onMounted,ref } from 'vue';
const getNode = inject<() => any>('getNode');
const data = ref({name: ''});
onMounted(() => {
  const node = getNode!();
  data.value = node.data;
});

</script>