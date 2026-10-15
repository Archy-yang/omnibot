import { request } from '../utils/request';
import type { ApiResponse } from '../types/api';

/**
 * 持久任务(25 号:周期调度宿主)。创建走对话内工具,这里只做列表+控制。
 */
export interface PersistentTaskItem {
  id: number;
  name: string;
  goal: string;
  /** 5 字段 cron(本地时区),如 "0 9 * * 1" = 每周一 9 点 */
  cron: string;
  enabled: boolean;
  next_run_at: string;
  last_run_at?: string;
  last_task_id?: number;
  fail_streak: number;
  /** 连续失败达到阈值被自动停用 */
  auto_paused: boolean;
  created_at: string;
}

export const persistentTaskService = {
  /** 持久任务列表(创建时间倒序) */
  async list(): Promise<PersistentTaskItem[]> {
    const response = await request.get<ApiResponse<{ tasks: PersistentTaskItem[] }>>(
      '/persistent-tasks',
    );
    return response.data.data.tasks;
  },

  /** 暂停(保留配置,可恢复) */
  async pause(id: number): Promise<void> {
    await request.post<ApiResponse<null>>(`/persistent-tasks/${id}/pause`);
  },

  /** 恢复(重算下次执行时间) */
  async resume(id: number): Promise<void> {
    await request.post<ApiResponse<null>>(`/persistent-tasks/${id}/resume`);
  },

  /** 删除(物理删除,不可恢复) */
  async remove(id: number): Promise<void> {
    await request.delete<ApiResponse<null>>(`/persistent-tasks/${id}`);
  },
};
