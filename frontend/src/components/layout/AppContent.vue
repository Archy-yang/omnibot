<script setup lang="ts">
import type { AppContentProps } from '@/types/components';

withDefaults(defineProps<AppContentProps>(), {
  maxWidth: '768px',
});
</script>

<template>
  <main class="flex-1">
    <div
      class="w-full mx-auto px-4 py-6 sm:px-6 lg:px-8"
      :style="{ maxWidth }"
    >
      <slot />
    </div>
  </main>
</template>
