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

 .text {
    display: flex;
    text-align: left;
    font-size: 14px;
    max-width: 250px;
    word-break: break-all;
}

</style>