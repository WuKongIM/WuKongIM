<template>
    <div class="text">
        {{ props.message.content.text }}
    </div>
</template>

<script setup lang="ts">

const props = defineProps<{
    message: any
}>()

</script>

<style scoped>
.text { text-align: left; font-size: 14px; line-height: 1.7; overflow-wrap: anywhere; white-space: pre-wrap; }
</style>
