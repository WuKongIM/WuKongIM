<template>
    <div class="w-full">
        <div class="flex">
            <div class="grow">
                <label class="form-control w-full max-w-xs">
                    <div class="label">
                        <span class="label-text font-semibold">同时在线人数</span>
                    </div>
                    <div class="text-xs text-red-500">模拟系统中同时在线用户数量</div>
                    <input type="number" class="input input-bordered w-full max-w-xs mt-2"
                        v-model="props.modelValue.onlineCount" />
                </label>
            </div>
            <div class="mr-6">
                <div class="flex gap-6">
                    <button class="btn btn-primary" v-on:click="onRun" :disabled="props.loading">
                        <label v-if="!props.loading">运行</label>
                        <span class="loading loading-spinner" v-if="props.loading"></span>
                    </button>
                    <button class="btn" v-on:click="onCancel">取消</button>
                </div>
                <div class="mt-4 gap-4">
                    <select class="select select-bordered w-full max-w-xs" v-on:change="onTemplateChange">
                        <option disabled selected>选择一个测试模版</option>
                        <option v-for="(tm,i) in templates" :value="i">{{tm.name}}</option>
                    </select>
                </div>
            </div>
        </div>
        <div class="w-full mt-6">
            <div class="form-control w-full">
                <div class="label">
                    <span class="label-text font-semibold flex items-center">
                        群聊
                        <button class="btn btn-circle btn-xs btn-outline ml-2 mt-[-0.2rem]"
                            v-on:click="() => onAddChannelItem()">
                            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                fill="none" stroke="currentColor" stroke-width="1" stroke-linecap="round"
                                stroke-linejoin="round" class="lucide lucide-plus">
                                <path d="M5 12h14" />
                                <path d="M12 5v14" />
                            </svg>
                        </button>
                    </span>
                </div>
                <div class="label">
                    <ul class="label-text text-xs text-red-500">
                        <li class="flex">
                            <div class="w-20">群数:</div>
                            <div>表示创建群的数量</div>
                        </li>
                        <li class="flex">
                            <div class="w-20">成员数:</div>
                            <div>表示每个群里的成员数量</div>
                        </li>
                        <li class="flex">
                            <div class="w-20">成员在线数:</div>
                            <div>表示每个群里的在线人数</div>
                        </li>
                        <li class="flex">
                            <div class="w-20">消息速率:</div>
                            <div>表示每个群里1分钟有多少条消息</div>
                        </li>
                        <li class="mt-2">例如： 群数:100 成员数: 200 成员在线人数: 50 消息速率: 60，
                            表示创建100个群人数在200人并且每个群有50个成员在线，每个群里1分钟发送60条消息</li>
                    </ul>
                </div>
                <div v-for="(item, i) in props.modelValue.channels">
                    <div class="flex gap-2">
                        <div>
                            <label class="form-control w-40">
                                <div class="label">
                                    <span class="label-text text-xs">群数</span>
                                </div>
                                <input type="number" class="input input-bordered w-full max-w-xs"
                                    v-model="item.count" />
                            </label>
                        </div>
                        <div>
                            <label class="form-control w-40">
                                <div class="label">
                                    <span class="label-text text-xs">成员数</span>
                                </div>
                                <input type="number" class="input input-bordered w-full max-w-xs"
                                    v-model="item.subscriberCount" />
                            </label>
                        </div>
                        <div>
                            <label class="form-control w-40">
                                <div class="label">
                                    <span class="label-text text-xs">成员在线数</span>
                                </div>
                                <input type="number" class="input input-bordered w-full max-w-xs"
                                    v-model="item.subscriberOnlineCount" />
                            </label>
                        </div>
                        <div>
                            <label class="form-control w-40">
                                <div class="label">
                                    <span class="label-text text-xs">消息速率（条/分钟）</span>
                                </div>
                                <input type="number" class="input input-bordered w-full max-w-xs"
                                    v-model="item.msgRate" />
                            </label>
                        </div>
                        <div class="flex items-center">
                            <button class="btn btn-circle btn-xs btn-outline ml-2 mt-[2rem]"
                                v-on:click="() => onRemoveChannelItem(i)">
                                <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"
                                    fill="none" stroke="currentColor" stroke-width="1" stroke-linecap="round"
                                    stroke-linejoin="round" class="lucide lucide-minus">
                                    <path d="M5 12h14" />
                                </svg>
                            </button>
                        </div>
                    </div>

                </div>


            </div>
        </div>
        <div class="w-full mt-4">
            <label class="form-control w-full">
                <div class="label">
                    <span class="label-text font-semibold">单聊</span>
                </div>
                <div class="label">
                    <ul class="label-text text-xs text-red-500">
                        <li class="flex">
                            <div class="w-20">数量:</div>
                            <div>表示有多少对个人聊天</div>
                        </li>
                        <li class="flex">
                            <div class="w-20">消息速率:</div>
                            <div>表示每对个人聊天里1分钟有多少条消息</div>
                        </li>
                        <li class="mt-2">例如： 数量: 1000 消息速率: 60， 表示有1000对单聊，每对单聊每分钟发送60条消息</li>
                    </ul>
                </div>
                <div>
                    <div class="flex gap-2">
                        <div>
                            <label class="form-control w-40">
                                <div class="label">
                                    <span class="label-text text-xs">数量</span>
                                </div>
                                <input type="number" class="input input-bordered w-full max-w-xs"
                                    v-model="props.modelValue.p2pItem.count" />
                            </label>
                        </div>
                        <div>
                            <label class="form-control w-40">
                                <div class="label">
                                    <span class="label-text text-xs">消息速率（条/分钟）</span>
                                </div>
                                <input type="number" class="input input-bordered w-full max-w-xs"
                                    v-model="props.modelValue.p2pItem.msgRate" />
                            </label>
                        </div>
                    </div>
                </div>
            </label>
        </div>

    </div>
</template>

<script setup lang="ts">
import { PropType } from 'vue';

import { ChannelItem, Config } from './Task';
import { taskToConfig } from '../services/Model';

const emit = defineEmits(['update:modelValue']);

const updateModelValue = (cfg:Config) => {
  emit('update:modelValue', cfg);
};

const props = defineProps({
    onCancel: {
        type: Function,
    },
    onRun: {
        type: Function as PropType<(cfg: Config) => void>,

    },
    modelValue: { // 配置
        type: Config,
        default: () => new Config()
    },
    loading: { // 运行按钮的loading
        type: Boolean,
        default: false
    },
    templates: { // 测试模版
        type: Array<any>,
        default: () => []
    }
})


// 移除群频道的item
const onRemoveChannelItem = (index: number) => {
    if (index > -1 && index < props.modelValue.channels.length) {
        props.modelValue.channels!.splice(index, 1);
    }
}

// 添加频道item
const onAddChannelItem = () => {
    const newItem = new ChannelItem()
    newItem.count = 0
    newItem.subscriberCount = 0
    newItem.subscriberOnlineCount = 0
    newItem.msgRate = 0
    props.modelValue.channels.push(newItem);
}

// 取消
const onCancel = () => {
    if (props.onCancel) {
        props.onCancel()
    }
}

// 运行
const onRun = () => {
    if (props.onRun) {
        props.onRun(props.modelValue)
    }
}

const onTemplateChange = (v:any) => {
    const index = v.target.value
   const template = props.templates[index]
   console.log("v--->",template)

   updateModelValue(taskToConfig(template.task))

}

</script>