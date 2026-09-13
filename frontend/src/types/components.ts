// ChatAvatar types
export type ChatAvatarSize = 'small' | 'medium' | 'large';
export type ChatAvatarRole = 'user' | 'assistant';

export type ChatAvatarProps = {
  role: ChatAvatarRole;
  size?: ChatAvatarSize;
};

// ChatMessage types
export type ChatMessageProps = {
  message: import('./chat').Message;
  showTime?: boolean;
};

// ChatMessageList types
export type ChatMessageListProps = {
  messages: import('./chat').Message[];
  isLoading?: boolean;
};

// ChatInput types
export type ChatInputProps = {
  modelValue: string;
  placeholder?: string;
  disabled?: boolean;
  isLoading?: boolean;
};

export type ChatInputEmits = {
  'update:modelValue': [value: string];
  send: [content: string];
};

// Sidebar types（dsh 左侧栏：常驻，可折叠，无遮罩/visible 概念）
export type SidebarProps = {
  /** 当前高亮的导航项：弹窗打开时对应高亮，都没开时为 chat */
  current?: 'chat' | 'memory' | 'subscriptions' | 'skills' | 'settings';
};

export type SidebarEmits = {
  'open-chat': [];
  'open-memory': [];
  'open-subscriptions': [];
  'open-skills': [];
  'open-settings': [];
};

// SettingsPanel types
export type SettingsPanelProps = {
  visible: boolean;
};

export type SettingsPanelEmits = {
  close: [];
  'update-config': [config: Record<string, unknown>];
};

// Toast types
export type ToastType = 'success' | 'error' | 'warning' | 'info';

export interface Toast {
  id: string;
  type: ToastType;
  message: string;
  duration?: number;
}

export type ToastProps = {
  toasts: Toast[];
};
