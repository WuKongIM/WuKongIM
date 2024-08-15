

<script setup lang="ts">


const props = defineProps<{
    message: any
}>()

const order = props.message.content

</script>

<template>
    <div class="order">
        <div class="orderNo">订单号：{{order.orderNo}}</div>
        <div class="orderMiddle">
            <div class="orderImg">
                <img :src="order.imgUrl" alt="" style="width: 40px;height: 40px;">
            </div>
            <div class="orderMiddleRight">
                    <div class="orderTitle">
                        {{order.title}}
                    </div>
                    <div class="orderPriceBox">
                        <div class="orderPrice">${{order.price}}</div>
                        <div class="orderNum">共{{order.num}}件</div>
                    </div>
            </div>
        </div>
    </div>
</template>

<style scoped>

.orderNo {
   display: flex;
   font-size: 12px;
}

.orderImg img{
    width: 40px;
    height: 40px;
    border-radius: 4px;
    background-size: cover;
}

.orderMiddle {
    display: flex;
    align-items: center;
    margin-top: 2px;
}

.orderMiddleRight {
    margin-left: 4px;
}

.orderPriceBox {
    display: flex;
    justify-content: space-between;
}

.orderTitle {
    font-size: 14px;
}

.orderPrice {
    font-size: 12px;
}

.orderNum {
    font-size: 12px;
}


</style>