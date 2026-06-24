/**
 * OmniBot 关于信息常量
 *
 * 用于设置面板「关于」Tab 展示。纯前端静态数据,不向后端要——
 * 版本号和入口接入状态都是发布期固定的事实,后端没有比前端更权威的来源。
 */

export interface ChannelInfo {
  /** 入口标识 */
  type: 'web' | 'wechat' | 'feishu';
  /** 显示名称 */
  label: string;
  /** 简介(一句话) */
  description: string;
  /** 接入状态 */
  status: '已接入' | '规划中';
}

export const APP_VERSION = 'v1.10.0';
export const APP_NAME = 'OmniBot';
export const APP_TAGLINE = '全平台智能助手';

/** 三入口接入状态(v1.10 阶段已经全部接入) */
export const CHANNELS: ChannelInfo[] = [
  {
    type: 'web',
    label: 'Web 网页',
    description: '浏览器端聊天 + 设置 + 长期记忆管理',
    status: '已接入',
  },
  {
    type: 'wechat',
    label: '微信公众号',
    description: '订阅号被动回复 + 文本命令(#模型设置 / #记住 等)',
    status: '已接入',
  },
  {
    type: 'feishu',
    label: '飞书机器人',
    description: '长连接 IM 单聊 + 跨入口记忆/配置共享',
    status: '已接入',
  },
];

/** 关于页面外链 */
export const ABOUT_LINKS = {
  repo: 'https://github.com/Archy-yang/omnibot',
} as const;
