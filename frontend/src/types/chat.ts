/**
 * 消息角色类型
 */
export type Role = 'user' | 'assistant' | 'system';

/**
 * 消息接口 - 与后端响应格式一致
 */
export interface AgentStep {
  step: number;
  tool_call?: string;
  tool_result?: string;
  final?: boolean;
}

export interface Message {
  id: number;
  role: Role;
  content: string;
  created_at: string;
  agentSteps?: AgentStep[];
}

/**
 * 聊天状态接口
 */
export interface ChatState {
  messages: Message[];
  isLoading: boolean;
  sessionId: string;
}
