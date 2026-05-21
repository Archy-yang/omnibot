<script setup lang="ts">
import { onMounted, watch } from 'vue';
import { useSettingsStore } from '@/stores/settings';

const settingsStore = useSettingsStore();

// Initialize theme on mount
onMounted(() => {
  applyTheme(settingsStore.theme);
});

// Watch for theme changes
watch(
  () => settingsStore.theme,
  (newTheme) => {
    applyTheme(newTheme);
  }
);

// Apply theme to document
const applyTheme = (theme: 'light' | 'dark') => {
  if (theme === 'dark') {
    document.documentElement.classList.add('dark');
  } else {
    document.documentElement.classList.remove('dark');
  }
};
</script>

<template>
  <div id="app">
    <router-view />
  </div>
</template>

<style>
/* Global resets and base styles are in style.css */
</style>
