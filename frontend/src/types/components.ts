// BaseIcon types
export type BaseIconProps = {
  name: string;
  size?: number;
  color?: string;
};

// BaseLoading types
export type BaseLoadingSize = 'small' | 'medium' | 'large';

export type BaseLoadingProps = {
  size?: BaseLoadingSize;
  color?: string;
};

// BaseButton types
export type BaseButtonSize = 'small' | 'medium' | 'large';
export type BaseButtonVariant =
  | 'primary'
  | 'secondary'
  | 'outline'
  | 'ghost'
  | 'danger';
export type BaseButtonType = 'button' | 'submit' | 'reset';

export type BaseButtonProps = {
  size?: BaseButtonSize;
  variant?: BaseButtonVariant;
  disabled?: boolean;
  loading?: boolean;
  fullWidth?: boolean;
  type?: BaseButtonType;
};

export type BaseButtonEmits = {
  click: [event: MouseEvent];
};

// BaseInput types
export type BaseInputSize = 'small' | 'medium' | 'large';
export type BaseInputType = 'text' | 'password' | 'email';
export type NInputType = 'text' | 'password' | 'textarea';

export type BaseInputProps = {
  modelValue: string;
  placeholder?: string;
  disabled?: boolean;
  type?: BaseInputType;
  size?: BaseInputSize;
};

export type BaseInputEmits = {
  'update:modelValue': [value: string];
  enter: [event: KeyboardEvent];
};

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
  send: [content: string, isAgentMode: boolean];
};

// AppHeader types
export type AppHeaderProps = {
  title?: string;
  showSettings?: boolean;
};

export type AppHeaderEmits = {
  'toggle-settings': [];
};

// AppContent types
export type AppContentProps = {
  maxWidth?: string;
};

// Sidebar types
export type SidebarProps = {
  visible: boolean;
  width?: string;
};

export type SidebarEmits = {
  close: [];
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

// MarkdownRenderer types
export type MarkdownRendererProps = {
  content: string;
};
