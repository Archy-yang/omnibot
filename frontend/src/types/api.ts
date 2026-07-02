import type { GetLLMProvidersResponse } from './llmProvider';

/**
 * 通用 API 响应接口 - 与后端响应格式一致
 */
export interface ApiResponse<T = void> {
  success: boolean;
  data: T;
  error?: string;
}

/**
 * 发送消息请求类型
 */
export interface SendMessageRequest {
  content: string;
}

/**
 * 获取历史记录响应数据结构
 */
export interface HistoryData<T> {
  messages: T[];
  has_more: boolean;
}

/**
 * 分页参数类型
 */
export interface PaginationParams {
  limit?: number;
  before?: number;
  after?: number;
}

/**
 * 获取历史记录响应类型
 */
export type GetHistoryResponse = HistoryData<import('./chat').Message>;

/**
 * Agent 工具调用事件（v1.5.2 流式协议）
 *
 * 后端 SSE 格式：
 *   event: tool_call
 *   data: {"tool": "rss_reader", "label": "读取了 RSS 订阅"}
 *
 * 前端按时序累积到 message.segments，渲染为对话中交错的思考条。
 * 没有工具调用时无 tool 段，UI 完全是纯文本（视觉等同纯聊天）。
 */
export interface ToolCallEvent {
  tool: string;
  label: string;
}

/**
 * Agent 工具结果事件（v1.5.3 流式协议）
 *
 * 后端 SSE 格式：
 *   event: tool_result
 *   data: {"tool": "get_current_time", "result": "10:30"}
 *
 * 紧跟在对应 tool_call 之后推送，供前端回填到思考条、点击展开查看。
 * 执行失败的结果已由后端脱敏为「工具执行失败」，不含原始 error。
 */
export interface ToolResultEvent {
  tool: string;
  result: string;
}

/**
 * Agent 完成事件
 */
export interface AgentDoneEvent {
  total_steps: number;
  duration_ms: number;
}

/**
 * LLM 配置接口
 */
export interface LLMConfig {
  provider: string;
  model: string;
  apiKey?: string;
  baseUrl?: string;
  temperature?: number;
  maxTokens?: number;
}

/**
 * 应用配置接口
 */
export interface Config {
  llm: LLMConfig;
  systemPrompt: string;
  contextWindow: number;
  enableMemory: boolean;
}

/**
 * 更新配置请求类型
 */
export type UpdateConfigRequest = Partial<Config>;

/**
 * 用户 LLM 配置响应类型
 */
export interface UserLLMConfigResponse {
  has_config: boolean;
  api_key_masked: string;
  base_url: string;
  model: string;
  provider: string;
  status_text: string;
  temperature: number;
  max_tokens: number;
}

/**
 * 更新用户 LLM 配置请求类型
 */
export interface UpdateUserLLMConfigRequest {
  provider: string;
  api_key?: string;
  base_url?: string;
  model: string;
  temperature?: number;
  max_tokens?: number;
}

/**
 * 长期记忆条目类型
 */
export interface MemoryItem {
  id: number;
  content: string;
  created_at: string;
}

/**
 * 获取长期记忆列表响应类型
 */
export interface GetMemoriesResponse {
  memories: MemoryItem[];
}

/**
 * 新增长期记忆请求类型
 */
export interface CreateMemoryRequest {
  content: string;
}

/**
 * 新增长期记忆响应类型
 */
export interface CreateMemoryResponse {
  message: string;
  memory: MemoryItem;
}

/**
 * 清空长期记忆响应类型
 */
export interface ClearMemoriesResponse {
  message: string;
}

/**
 * 删除单条长期记忆响应类型
 */
export interface DeleteMemoryResponse {
  message: string;
}

/**
 * 更新长期记忆请求类型
 */
export interface UpdateMemoryRequest {
  content: string;
}

/**
 * 更新长期记忆响应类型
 */
export interface UpdateMemoryResponse {
  message: string;
  memory: MemoryItem;
}

/**
 * 用户 LLM 服务商列表响应类型
 */
export type UserLLMProvidersResponse = GetLLMProvidersResponse;
