<script name="ToolBarRight" lang="ts" setup>
import Avatar from './components/Avatar.vue';
</script>

<template>
  <div class="tool-bar-ri">
    <div class="header-icon"></div>
    <Avatar id="avatar" />
  </div>
</template>

<style scoped lang="scss">
.tool-bar-ri {
  display: flex;
  align-items: center;
  justify-content: center;
  .header-icon {
    display: flex;
    align-items: center;
    & > * {
      margin-left: 20px;
      color: var(--el-header-text-color);
    }
  }
}
</style>
