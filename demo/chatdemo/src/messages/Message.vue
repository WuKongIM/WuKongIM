<script setup lang="ts">

import { Message, MessageContentType } from 'wukongimjssdk';
import Text from './Text.vue'
import CustomMessage from './CustomMessage.vue'
import { orderMessage } from './CustomMessage'

const props = defineProps<{
    message: any
}>()

const contentType = props.message.content.contentType


</script>

<template>
    <div>
        <Text :message="$props.message" v-if="contentType === MessageContentType.text"></Text>
        <CustomMessage :message="$props.message" v-if="contentType === orderMessage" ></CustomMessage>
    </div>
</template>