import { request } from '../utils/request';
import type {
  ApiResponse,
  ListSkillsResponse,
  UpdateSkillResponse,
} from '../types/api';

/**
 * 技能管理服务(13-插件系统):清单 + 启停。
 * 启停即时生效(后端重建工具池),无需刷新对话。
 */
export const skillService = {
  async listSkills(): Promise<ListSkillsResponse> {
    try {
      const response = await request.get<ApiResponse<ListSkillsResponse>>('/skills');
      return response.data.data;
    } catch (error) {
      console.error('Failed to list skills:', error);
      throw error;
    }
  },

  async updateSkill(name: string, enabled: boolean): Promise<UpdateSkillResponse> {
    try {
      const response = await request.put<ApiResponse<UpdateSkillResponse>>(
        `/skills/${encodeURIComponent(name)}`,
        { enabled }
      );
      return response.data.data;
    } catch (error) {
      console.error('Failed to update skill:', error);
      throw error;
    }
  },
};

export default skillService;
