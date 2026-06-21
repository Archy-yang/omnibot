/**
 * 消息角色类型
 */
export type Role = 'user' | 'assistant' | 'system';

/**
 * 消息段落（v1.5.3）
 *
 * 把一条助手消息按 LLM 真实输出时序拆成有序段落序列，使「文本 → 调工具 → 文本」
 * 能按发生顺序交错渲染，而不是「工具条全堆顶部、文本全拼底部」。
 *
 * - text：一段连续的文本（markdown）
 * - tool：一次工具调用。result 在工具返回前为 undefined（UI 显示「正在调用…」），
 *   返回后回填（已脱敏）。expanded 控制是否展开看 result。
 *
 * 仅活在本次会话内存中，不持久化；刷新页面后历史消息无 segments，回退渲染 content。
 */
export type MessageSegment =
  | { type: 'text'; content: string }
  | {
      type: 'tool';
      tool: string;
      label: string;
      result?: string;
      expanded?: boolean;
    };

export interface Message {
  id: number;
  role: Role;
  content: string;
  created_at: string;
  /**
   * 本次会话内按时序记录的渲染段落（v1.5.3）。
   * 仅 role==='assistant' 且本轮经过流式时存在；历史/刷新后的消息无此字段，
   * UI 回退用 content 渲染。
   */
  segments?: MessageSegment[];
}

/**
 * 聊天状态接口
 */
export interface ChatState {
  messages: Message[];
  isLoading: boolean;
  sessionId: string;
}
