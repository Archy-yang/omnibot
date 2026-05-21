<script setup lang="ts">
import { computed } from 'vue';
import { NInput } from 'naive-ui';
import type { BaseInputProps, BaseInputEmits, NInputType } from '@/types';

const props = withDefaults(defineProps<BaseInputProps>(), {
  placeholder: '',
  disabled: false,
  type: 'text',
  size: 'medium',
});

const emit = defineEmits<BaseInputEmits>();

// Size mapping
const sizeMap = {
  small: 'small',
  medium: 'medium',
  large: 'large',
} as const;

const inputSize = computed(() => sizeMap[props.size]);

// Map our type to Naive UI type and set native input type
const nInputType = computed<NInputType>(
  () => (props.type === 'email' ? 'text' : props.type) as NInputType
);

const inputProps = computed(() => ({
  type: props.type,
}));

const handleInput = (value: string) => {
  emit('update:modelValue', value);
};

const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Enter') {
    emit('enter', event);
  }
};
</script>

<template>
  <NInput
    :value="modelValue"
    :type="nInputType"
    :input-props="inputProps"
    :placeholder="placeholder"
    :disabled="disabled"
    :size="inputSize"
    class="base-input"
    @input="handleInput"
    @keydown="handleKeydown"
  >
    <template v-if="$slots.prefix" #prefix>
      <slot name="prefix" />
    </template>
    <template v-if="$slots.suffix" #suffix>
      <slot name="suffix" />
    </template>
  </NInput>
</template>

<style scoped>
.base-input {
  width: 100%;
}

.base-input :deep(.n-input__input-el) {
  transition: all 0.2s ease;
}

.base-input :deep(.n-input__input-el:focus) {
  border-color: var(--accent);
  box-shadow: 0 0 0 2px var(--accent-bg);
}

.base-input :deep(.n-input--disabled) {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
