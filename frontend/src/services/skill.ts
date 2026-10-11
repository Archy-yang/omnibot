import { request } from '../utils/request';
import type {
  ApiResponse,
  ListSkillsResponse,
  SkillEditPayload,
  SkillFileInput,
  SkillItem,
} from '../types/api';

/** 技能详情(编辑回显,含 instruction 与 files) */
export interface SkillDetail extends SkillItem {
  instruction: string;
  files: SkillFileInput[];
}

/**
 * 技能管理服务(18-Skill 技能系统):CRUD。
 * 目录/启停即时生效(后端 CRUD 失效缓存并重建主 Agent 技能目录),无需刷新对话。
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

  async getSkill(id: number): Promise<SkillDetail> {
    try {
      const response = await request.get<ApiResponse<SkillDetail>>(`/skills/${id}`);
      return response.data.data;
    } catch (error) {
      console.error('Failed to get skill:', error);
      throw error;
    }
  },

  async createSkill(payload: SkillEditPayload): Promise<SkillItem> {
    try {
      const response = await request.post<ApiResponse<SkillItem>>('/skills', payload);
      return response.data.data;
    } catch (error) {
      console.error('Failed to create skill:', error);
      throw error;
    }
  },

  async updateSkill(id: number, payload: SkillEditPayload): Promise<void> {
    try {
      await request.put<ApiResponse<null>>(`/skills/${id}`, payload);
    } catch (error) {
      console.error('Failed to update skill:', error);
      throw error;
    }
  },

  async deleteSkill(id: number): Promise<void> {
    try {
      await request.delete<ApiResponse<null>>(`/skills/${id}`);
    } catch (error) {
      console.error('Failed to delete skill:', error);
      throw error;
    }
  },
};

export default skillService;
