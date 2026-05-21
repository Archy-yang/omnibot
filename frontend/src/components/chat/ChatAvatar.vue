<script setup lang="ts">
import { computed } from 'vue';
import type { ChatAvatarProps, ChatAvatarSize } from '@/types';

const props = withDefaults(defineProps<ChatAvatarProps>(), {
  size: 'medium',
});

const sizeMap: Record<ChatAvatarSize, number> = {
  small: 28,
  medium: 30,
  large: 36,
};

const avatarSize = computed(() => sizeMap[props.size]);
const isUser = computed(() => props.role === 'user');
</script>

<template>
  <div
    class="flex items-center justify-center rounded-sm flex-shrink-0 select-none"
    :class="isUser ? 'bg-[#5436da]' : 'bg-[#19c37d]'"
    :style="{ width: `${avatarSize}px`, height: `${avatarSize}px` }"
  >
    <!-- User Icon -->
    <svg
      v-if="isUser"
      viewBox="0 0 24 24"
      fill="none"
      stroke="white"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      :width="avatarSize * 0.55"
      :height="avatarSize * 0.55"
    >
      <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
      <circle cx="12" cy="7" r="4"/>
    </svg>

    <!-- Assistant Icon -->
    <svg
      v-else
      viewBox="0 0 41 41"
      fill="white"
      :width="avatarSize * 0.65"
      :height="avatarSize * 0.65"
    >
      <path d="M37.5324 16.8707c.9886-2.9594.6594-6.2024-.9027-8.8945-2.348-4.0728-7.0494-6.1685-11.62-5.1791C22.6324 1.0184 19.7864.0035 16.9043.0035c-4.7113 0-8.8888 3.0182-10.3375 7.4773C3.4754 8.4661 1.0148 10.7402.0093 13.7066c-2.354 4.0712-2.1146 9.2009.5901 12.9772-.9886 2.9595-.6594 6.2024.9027 8.8946 2.348 4.0727 7.0494 6.1684 11.62 5.179 2.3792 2.5777 5.226 3.5926 8.1081 3.5926 4.7113 0 8.8888-3.0181 10.3375-7.4773 2.9508-.9852 5.4114-3.2594 6.4169-6.2258 2.3539-4.0712 2.1145-9.2009-.5902-12.9772zM22.4068 38.6485a7.704 7.704 0 01-4.9486-1.7912l.2442-.1382 8.2147-4.7457a1.3464 1.3464 0 00.6772-1.1689v-11.5853l3.4729 2.0061a.1236.1236 0 01.0673.0962v9.5933a7.7397 7.7397 0 01-7.7277 7.7337zM5.8108 31.5408a7.7011 7.7011 0 01-.9219-5.1768l.2443.1463 8.2147 4.7458a1.3393 1.3393 0 001.3543 0l10.0314-5.7918v4.0114a.1262.1262 0 01-.0518.1077l-8.3001 4.7886a7.7355 7.7355 0 01-10.5709-2.8312z"/>
    </svg>
  </div>
</template>
