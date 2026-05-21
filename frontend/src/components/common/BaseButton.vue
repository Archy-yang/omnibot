<script setup lang="ts">
import { computed } from 'vue';
import { NButton } from 'naive-ui';
import type { BaseButtonProps, BaseButtonEmits } from '@/types';
import BaseLoading from './BaseLoading.vue';

const props = withDefaults(defineProps<BaseButtonProps>(), {
  size: 'medium',
  variant: 'primary',
  disabled: false,
  loading: false,
  fullWidth: false,
  type: 'button',
});

const emit = defineEmits<BaseButtonEmits>();

// Map our variant to Naive UI types
const variantMap = {
  primary: { type: 'primary', ghost: false, text: false },
  secondary: { type: 'default', ghost: false, text: false },
  outline: { type: 'default', ghost: true, text: false },
  ghost: { type: 'default', ghost: false, text: true },
  danger: { type: 'error', ghost: false, text: false },
};

const buttonType = computed(() => variantMap[props.variant].type as 'primary' | 'default' | 'error' | undefined);
const buttonGhost = computed(() => variantMap[props.variant].ghost);
const buttonText = computed(() => variantMap[props.variant].text);

// Size mapping
const sizeMap = {
  small: 'small',
  medium: 'medium',
  large: 'large',
} as const;

const buttonSize = computed(() => sizeMap[props.size]);

const handleClick = (event: MouseEvent) => {
  if (!props.disabled && !props.loading) {
    emit('click', event);
  }
};
</script>

<template>
  <NButton
    :type="buttonType"
    :size="buttonSize"
    :ghost="buttonGhost"
    :text="buttonText"
    :disabled="disabled || loading"
    :block="fullWidth"
    :attr-type="type"
    class="base-button"
    @click="handleClick"
  >
    <template v-if="loading" #icon>
      <BaseLoading size="small" />
    </template>
    <template v-else-if="$slots.icon" #icon>
      <slot name="icon" />
    </template>
    <slot />
  </NButton>
</template>

<style scoped>
.base-button {
  transition: all 0.2s ease;
}

.base-button:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: var(--shadow);
}

.base-button:active:not(:disabled) {
  transform: translateY(0);
}

.base-button:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
