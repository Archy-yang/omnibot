-- v1.8 一次性数据库迁移脚本:删除遗留的 wechat_accounts 表
--
-- 背景:v1.8 起 WechatAccount 双轨已彻底删除,身份解析统一走 user_channels。
-- 新部署不再 AutoMigrate 该表;已部署环境的 wechat_accounts 表为残留,占用空间但不影响功能。
--
-- 执行时机:**可手动执行,不执行也行**——只是占点表空间。
--
-- 使用方法:
--   psql -h <host> -U <user> -d omnibot -f scripts/migrate_v1.8_drop_wechat_accounts.sql
--
-- 注意:此操作不可逆,执行前请确认该表里没有需要迁移的活跃数据。
-- 项目仍在 MVP/dev 期,wechat_accounts 表通常无活跃用户依赖。生产环境如有真实数据,
-- 请先用以下查询确认:
--   SELECT COUNT(*) FROM wechat_accounts;
--   SELECT * FROM wechat_accounts LIMIT 10;
-- 若有数据,需另写迁移脚本平移到 user_channels 后再 DROP。

DROP TABLE IF EXISTS wechat_accounts;
