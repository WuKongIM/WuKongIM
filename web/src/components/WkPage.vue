<template>
  <div class="wk-page">
    <slot />
  </div>
</template>
<script lang="ts" name="WkPage" setup></script>

<style lang="scss" scoped>
.wk-page {
  width: 100%;
  height: 100%;
  padding: 12px;
  box-sizing: border-box;
}
</style>
