-- 经营历史的余额类指标（用户总余额、上游总余额）此前只有"当前值"：
-- users.balance 与 accounts.extra.upstream_billing_probe 都是被覆盖写的最新快照，数据库里没有历史。
-- 为了让这两个指标也能按日期区间查询，这里在既有的永久经营日汇总表上追加每日余额快照列，
-- 由每日聚合作业在"当天"那一行写入，查询时取区间内最后一个非空快照作为期末余额。
--
-- 复用 dashboard_business_daily 而不是新建表的原因：
--   1. 主键同为北京时间自然日，语义完全一致，查询无需再做一次 JOIN；
--   2. 既有聚合 upsert 的列清单是显式枚举的，不含这些新列，因此不会被回溯重算覆盖；
--   3. finalized_at 固化逻辑可直接复用，历史行一旦固化就不再被改写。
--
-- 注意：历史余额无法回填，该指标从本次上线当天开始累积。
ALTER TABLE dashboard_business_daily
    ADD COLUMN IF NOT EXISTS user_balance_total DECIMAL(20, 10),
    ADD COLUMN IF NOT EXISTS upstream_balance_total DECIMAL(20, 10),
    ADD COLUMN IF NOT EXISTS balance_captured_at TIMESTAMPTZ;

COMMENT ON COLUMN dashboard_business_daily.user_balance_total IS '当日采集到的用户总余额快照（排除管理员与未真实充值用户）；NULL 表示该日无快照。';
COMMENT ON COLUMN dashboard_business_daily.upstream_balance_total IS '当日采集到的上游总余额快照（按上游站点去重后求和）；NULL 表示该日无快照。';
COMMENT ON COLUMN dashboard_business_daily.balance_captured_at IS '当日余额快照的最后一次采集时间。';
