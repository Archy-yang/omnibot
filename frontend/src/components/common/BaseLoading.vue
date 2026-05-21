<script setup lang="ts">
import { computed } from 'vue';
import type { BaseLoadingProps } from '@/types';

const props = withDefaults(defineProps<BaseLoadingProps>(), {
  size: 'medium',
  color: undefined,
});

const sizeMap = {
  small: 16,
  medium: 24,
  large: 32,
};

const spinnerSize = computed(() => sizeMap[props.size]);
</script>

<template>
  <div class="base-loading flex items-center justify-center gap-2">
    <div
      class="loading-spinner"
      :style="{
        width: `${spinnerSize}px`,
        height: `${spinnerSize}px`,
        borderColor: color || 'currentColor',
      }"
    ></div>
    <slot>
      <span v-if="$slots.default" class="loading-text">
        <slot></slot>
      </span>
    </slot>
  </div>
</template>

<style scoped>
.base-loading {
  color: var(--text);
}

.loading-spinner {
  border: 2px solid transparent;
  border-top-color: currentColor;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.loading-text {
  font-size: 14px;
  color: var(--text);
}

@keyframes spin {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}
</style>
