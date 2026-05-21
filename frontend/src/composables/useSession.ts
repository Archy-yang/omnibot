import { computed } from 'vue';
import { useUserStore } from '../stores/user';
import { useChatStore } from '../stores/chat';

/**
 * 会话管理 Composable
 * 封装 userStore 和 chatStore 的会话相关逻辑
 */
export function useSession() {
  const userStore = useUserStore();
  const chatStore = useChatStore();

  // State - 从 userStore 获取会话 ID
  const sessionId = computed<string>(() => userStore.sessionId);

  /**
   * 初始化会话
   * 从 localStorage 恢复或生成新的会话 ID
   * 同时同步到 chatStore
   */
  const initSession = (): void => {
    userStore.initSession();
    // 同步会话 ID 到 chatStore
    if (userStore.sessionId && chatStore.sessionId !== userStore.sessionId) {
      chatStore.setSessionId(userStore.sessionId);
    }
  };

  /**
   * 创建新会话
   * 生成新的会话 ID，清空当前聊天记录
   */
  const newSession = (): void => {
    const newSessionId = `session-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
    localStorage.setItem('session-id', newSessionId);
    userStore.sessionId = newSessionId;
    chatStore.setSessionId(newSessionId);
    chatStore.clearMessages();
  };

  /**
   * 清除会话
   * 清空会话 ID 和聊天记录，移除 localStorage 中的会话信息
   */
  const clearSession = (): void => {
    userStore.logout();
    chatStore.setSessionId('');
    chatStore.clearMessages();
  };

  return {
    // State
    sessionId,
    // Methods
    initSession,
    newSession,
    clearSession,
  };
}
