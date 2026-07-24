/**
 * 消息角色类型
 */
export type Role = 'user' | 'assistant' | 'system';

/**
 * 消息段落（v1.5.3 / 思考模式改造）
 *
 * 把一条助手消息按 LLM 真实输出时序拆成有序段落序列，使「文本 -> 调工具 -> 文本」
 * 能按发生顺序交错渲染，而不是「工具条全堆顶部、文本全拼底部」。
 *
 * - text：一段连续的文本（markdown）
 * - tool：一次工具调用。result 在工具返回前为 undefined（UI 显示「正在调用…」），
 *   返回后回填（已脱敏）。expanded 控制是否展开看 result。
 *
 * 思考模式改造后,segments 的语义:
 * - 最后一个 text 段 = 最终回复(主气泡渲染)
 * - 其余所有段(前面的 text + 全部 tool)= 思考过程(灰色可折叠思考块)
 * - segments 由后端持久化并随 history 返回,刷新/历史回看仍可用
 */
export type MessageSegment =
  | { type: 'text'; content: string; role?: 'thought' | 'final' }
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
   * 助手消息按时序记录的渲染段落（v1.5.3）。
   * 流式产生时由 store 维护;后端持久化后历史消息也会带回 segments。
   * 无 segments 的老消息回退用 content 渲染。
   */
  segments?: MessageSegment[];
  /**
   * 是否处于流式输出中(思考模式改造)。
   * true 时思考块强制展开实时显示过程;流式结束(Done/Error)置 false,思考块自动收起。
   * 历史消息无此字段(默认 falsy),视为已结束。
   */
  streaming?: boolean;
  /** 消息种类:"report"=主 Agent 主动汇报子任务结果(后端落库,刷新后历史仍还原);空=普通对话 */
  kind?: string;
  /** 汇报消息关联的后台任务 ID(kind==="report" 时有,可点开看对应子任务) */
  task_id?: number;
}

/**
 * 聊天状态接口
 */
export interface ChatState {
  messages: Message[];
  isLoading: boolean;
  sessionId: string;
}
