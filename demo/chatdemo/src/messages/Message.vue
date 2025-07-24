<script setup lang="ts">

import { Message, MessageContentType } from 'wukongimjssdk';
import Text from './Text.vue'
import CustomMessage from './CustomMessage.vue'
import { orderMessage } from './CustomMessage'
import Stream from './Stream.vue';

const props = defineProps<{
    message: any
}>()

const contentType = props.message.content.contentType
const streamOn = props.message.streamOn



</script>

<template>
    <div>
        <Stream :message="$props.message" v-if="streamOn"></Stream>
        <Text :message="$props.message" v-else-if="contentType === MessageContentType.text"></Text>
        <CustomMessage :message="$props.message" v-else-if="contentType === orderMessage" ></CustomMessage>
    </div>
</template>