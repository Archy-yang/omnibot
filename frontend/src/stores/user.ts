import { defineStore } from 'pinia';
import { ref } from 'vue';
import type { User } from '../types/user';

const SESSION_ID_KEY = 'session-id';

export const useUserStore = defineStore(
  'user',
  () => {
    // State
    const user = ref<User | null>(null);
    const isAuthenticated = ref<boolean>(false);
    const sessionId = ref<string>('');

    // Getters

    // Actions
    const initSession = (): void => {
      const savedSessionId = localStorage.getItem(SESSION_ID_KEY);
      if (savedSessionId) {
        sessionId.value = savedSessionId;
        isAuthenticated.value = true;
      } else {
        // Generate a new session ID if none exists
        sessionId.value = `session-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
        localStorage.setItem(SESSION_ID_KEY, sessionId.value);
        isAuthenticated.value = true;
      }
    };

    const logout = (): void => {
      user.value = null;
      isAuthenticated.value = false;
      sessionId.value = '';
      localStorage.removeItem(SESSION_ID_KEY);
    };

    const setUser = (userData: User): void => {
      user.value = userData;
      isAuthenticated.value = true;
    };

    return {
      // State
      user,
      isAuthenticated,
      sessionId,
      // Actions
      initSession,
      logout,
      setUser,
    };
  },
  {
    persist: {
      key: 'user-store',
      storage: localStorage,
      pick: ['sessionId', 'isAuthenticated'],
    },
  }
);
