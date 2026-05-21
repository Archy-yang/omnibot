import { ref } from 'vue';

/**
 * Toast 类型
 */
export type ToastType = 'success' | 'error' | 'warning' | 'info';

/**
 * Toast 消息接口
 */
export interface Toast {
  id: string;
  type: ToastType;
  message: string;
  duration: number;
}

// 全局 Toast 列表状态
const toasts = ref<Toast[]>([]);

/**
 * 默认显示时长（毫秒）
 */
const DEFAULT_DURATION = 3000;

/**
 * 生成唯一 ID
 */
const generateId = (): string => {
  return `toast-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
};

/**
 * 消息提示 Composable
 * 提供 Toast 消息提示功能，支持四种类型
 */
export function useToast() {
  /**
   * 显示 Toast 消息
   * @param message 消息内容
   * @param type 消息类型
   * @param duration 显示时长（毫秒）
   */
  const showToast = (
    message: string,
    type: ToastType = 'info',
    duration: number = DEFAULT_DURATION
  ): string => {
    const id = generateId();
    const toast: Toast = {
      id,
      type,
      message,
      duration,
    };

    toasts.value.push(toast);

    // 自动超时隐藏
    if (duration > 0) {
      setTimeout(() => {
        hideToast(id);
      }, duration);
    }

    return id;
  };

  /**
   * 隐藏指定的 Toast 消息
   * @param id Toast ID
   */
  const hideToast = (id: string): void => {
    const index = toasts.value.findIndex((t) => t.id === id);
    if (index > -1) {
      toasts.value.splice(index, 1);
    }
  };

  /**
   * 显示成功消息
   * @param message 消息内容
   * @param duration 显示时长
   */
  const success = (message: string, duration?: number): string => {
    return showToast(message, 'success', duration);
  };

  /**
   * 显示错误消息
   * @param message 消息内容
   * @param duration 显示时长
   */
  const error = (message: string, duration?: number): string => {
    return showToast(message, 'error', duration);
  };

  /**
   * 显示警告消息
   * @param message 消息内容
   * @param duration 显示时长
   */
  const warning = (message: string, duration?: number): string => {
    return showToast(message, 'warning', duration);
  };

  /**
   * 显示信息消息
   * @param message 消息内容
   * @param duration 显示时长
   */
  const info = (message: string, duration?: number): string => {
    return showToast(message, 'info', duration);
  };

  return {
    // State
    toasts,
    // Methods
    showToast,
    hideToast,
    success,
    error,
    warning,
    info,
  };
}
