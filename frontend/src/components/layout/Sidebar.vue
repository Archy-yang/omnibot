<script setup lang="ts">
import type { SidebarProps, SidebarEmits } from '@/types/components';

withDefaults(defineProps<SidebarProps>(), {
  width: '320px',
});
defineEmits<SidebarEmits>();
</script>

<template>
  <Teleport to="body">
    <Transition name="sidebar-overlay">
      <div
        v-if="visible"
        class="fixed inset-0 bg-black/30 z-40"
        @click="$emit('close')"
      />
    </Transition>

    <Transition name="sidebar-slide">
      <aside
        v-if="visible"
        class="fixed top-0 right-0 h-full bg-white dark:bg-gray-900 shadow-xl z-50 flex flex-col"
        :style="{ width }"
      >
        <slot />
      </aside>
    </Transition>
  </Teleport>
</template>

<style scoped>
.sidebar-slide-enter-from,
.sidebar-slide-leave-to {
  transform: translateX(100%);
}

.sidebar-slide-enter-active,
.sidebar-slide-leave-active {
  transition: transform 0.3s ease-in-out;
}

.sidebar-overlay-enter-from,
.sidebar-overlay-leave-to {
  opacity: 0;
}

.sidebar-overlay-enter-active,
.sidebar-overlay-leave-active {
  transition: opacity 0.3s ease-in-out;
}
</style>
