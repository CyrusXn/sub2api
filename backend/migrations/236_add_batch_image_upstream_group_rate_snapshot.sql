-- 批量生图的既有 group_rate_multiplier 可能已被用户专属或图片独立倍率覆盖，需独立保存分组默认上游倍率。
ALTER TABLE batch_image_jobs
    ADD COLUMN IF NOT EXISTS upstream_group_rate_multiplier DECIMAL(10, 4);

COMMENT ON COLUMN batch_image_jobs.upstream_group_rate_multiplier IS '提交时所属分组默认上游倍率快照；NULL 表示旧任务未保存该快照。';
