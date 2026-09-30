<script setup lang="ts">

import { Message, MessageContentType } from 'wukongimjssdk';
import Text from './Text.vue'
import CustomMessage from './CustomMessage.vue'
import { orderMessage } from './CustomMessage'
import { computed } from 'vue';
import { t } from '../i18n';

const props = defineProps<{
    message: any
}>()

const contentType = computed(() => props.message.content?.contentType)


</script>

<template>
    <div>
        <span v-if="message.contentStale">{{ t('staleMessage') }}</span>
        <Text :message="$props.message" v-else-if="contentType === MessageContentType.text"></Text>
        <CustomMessage :message="$props.message" v-else-if="contentType === orderMessage" ></CustomMessage>
        <span v-else>{{ t('unknownMessageDigest') }}</span>
        <small v-if="!message.contentStale && message.updatedAtMs" class="edited-label">{{ t('edited') }}</small>
    </div>
</template>
<style scoped>
.edited-label { display: block; margin-top: 4px; font-size: 11px; opacity: .8; text-align: right; }
</style>
