-- v1.11 清理:删除孤儿表 conversation_digests(M8.4 digests 死代码清理的遗留收尾)
--
-- 背景:d863611(M8.4,2026-09-26)删除了 digests 全部代码(DigestRepository/ConversationDigest
-- domain/AutoMigrate 注册),commit 明确记录"表本身保留不删,仅代码不再引用"。本脚本完成收尾。
-- 删除前已核实(2026-10-11):internal/ 全仓无任何 conversation_digests/ConversationDigest 代码引用。
-- 同名易混淆的两张表为在用表,不在此列:digest_watermarks(沉淀管线自身水位)、
-- embedding_watermarks(消息嵌入水位,M7)。
--
-- 存量数据留档:2 行旧对话摘要(2026-09-10 生成,代码下线前)。summary 全文留档如下;
-- embedding 向量为摘要的派生数据(可由 embedding 模型对 summary 重新生成)且属死表字段,
-- 不留档。恢复方式:按 git 历史 internal/domain/memory 的 ConversationDigest 定义重建表后
-- 执行下列语句。

-- 执行记录:2026-10-11 于 omnibot_test 库执行,影响 1 张表、2 行存档。

-- INSERT INTO conversation_digests (id, user_id, summary, embedding, embedding_model,
--   from_message_id, to_message_id, msg_count, status, created_at) VALUES
-- (1, 1, '用户与助手围绕2026年国庆（9月27日至10月4日）西双版纳9人家庭游进行规划。助手先给出航班、住宿、行程、预算等初步方案，用户质疑费用后，助手解释国庆溢价并给省钱建议。用户告知已订机票和4室别墅，要求聚焦必去景点。助手给出必去清单、老人儿童门票优惠及8天7晚落地行程表。用户认为包车花费高，助手核算打车费用并推荐"远途包车+市区打车"混合方案；用户又问租两辆5座车费用，助手曾误判超员，经纠正后重新对比，结论是租车不如打车划算。后用户要求调研最新情况，后台联网检索连续失败，实时价格未能核实，只能提供常规参考价。未决事项包括交通方式未最终确定、实时租车和票价待临行前自行核实。最后用户提到正在奥北森林公园充电，未继续旅行讨论。', NULL /* 向量省略 */, 'openai_compatible/qwen3-embedding-4b', 1, 40, 40, 'active', '2026-09-10 01:10:55.829663+00');
-- (2, 1, '对话围绕电动车充电记录、飞书文档与MCP接入、电耗分析展开。用户提供本次充电数据（30%→98%、65.86度、15.91元、里程5475公里），助手修正电池容量推算，确认为78度；并在飞书《充电记录》多维表格中读取23条记录、补录新记录，日期经用户确认为2026-09-04（上周五）。用户希望直接连接飞书官方MCP、不起本地中转，调研显示可行但端点与配置待核实。充电总花费为24笔1227.90元，约0.225元/公里。实测电耗约21.6（桩显）/18.8（电池侧）kWh/100km，但官方海狮06参数未查到，合理性暂无定论。未决：具体充电时刻未记录，天气查询已安排待回报。', NULL /* 向量省略 */, 'openai_compatible/qwen3-embedding-4b', 41, 80, 40, 'active', '2026-09-10 01:12:14.208339+00');

DROP TABLE IF EXISTS conversation_digests;
