-- migrate_v1.9_episode_to_fact.sql
-- §14.2.3 / M8.4:episode 经历层被 conversation_chunks 取代,沉淀已断源(prompt 不再引导
-- episode);本脚本把存量 episode 行显式归一为 fact。不可逆(kind 无历史列)。
--
-- 执行记录:2026-09-26 已在开发库手工执行,受影响 1 行(id=38,user_id=1,
-- source_message_id=186),原文与回滚边界留档于 docs/30-服务架构/01-高层设计/
-- 12-记忆系统技术方案.md §14.2.3。本脚本为其可复现产物(架构复评 P2-8),
-- 供其他环境重放或 CI 演练。
--
-- 注意:新代码路径已有等价归一(domain/memory.NormalizeKind + 调用侧 warn),
-- 本脚本仅针对存量数据;M8.4 后沉淀不再产生新 episode 行。

-- 前置核查:确认受影响行(执行前人工留档)
-- SELECT id, user_id, source_message_id, content, created_at FROM memories WHERE kind = 'episode';

UPDATE memories SET kind = 'fact' WHERE kind = 'episode';

-- 后置核查:应返回 0
-- SELECT count(*) FROM memories WHERE kind = 'episode';
