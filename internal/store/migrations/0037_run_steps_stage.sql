-- 运行步骤下沉到节点(job)级:run_steps 增 stage 列记录所属阶段名,
-- 供运行详情按「阶段 → 节点」两级聚合(进度图阶段框 + 步骤详情分组)。
-- 旧行 stage 为空串 → 前端回退为单级展示,无数据迁移负担。
ALTER TABLE run_steps ADD COLUMN stage TEXT NOT NULL DEFAULT '';
