<script setup lang="ts">

import { Message, MessageContentType } from 'wukongimjssdk';
import Text from './Text.vue'
import CustomMessage from './CustomMessage.vue'
import { orderMessage } from './CustomMessage'
import Stream from './Stream.vue';
import { computed } from 'vue';
import { t } from '../i18n';

const props = defineProps<{
    message: any
}>()

const contentType = computed(() => props.message.content.contentType)
const streamOn = computed(() => props.message.setting.streamOn)


</script>

<template>
    <div>
        <span v-if="message.contentStale">{{ t('staleMessage') }}</span>
        <Stream :message="$props.message" v-else-if="streamOn"></Stream>
        <Text :message="$props.message" v-else-if="contentType === MessageContentType.text"></Text>
        <CustomMessage :message="$props.message" v-else-if="contentType === orderMessage" ></CustomMessage>
        <small v-if="!message.contentStale && message.updatedAtMs" class="edited-label">{{ t('edited') }}</small>
    </div>
</template>
<style scoped>
.edited-label { display: block; margin-top: 4px; font-size: 11px; opacity: .8; text-align: right; }
</style>
