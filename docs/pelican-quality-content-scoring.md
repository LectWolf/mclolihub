# 鹈鹕 HTML 内容评分

当前检测采用“生成 HTML → 独立 AI 内容评分”，两次请求均使用 high 推理。生成预算 900 秒，评分预算 180 秒。评分阶段读取 HTML/SVG/CSS/SMIL 源码，不调用截图渲染器。

评分借用 tihu 的五项权重：鹈鹕 25%、自行车 25%、骑姿 20%、构图 15%、错误 15%。AI 返回每项 1–10 的整数，后端严格校验字段、范围、重复字段和结尾，再将加权结果换算为 0–100 分。

本项目的单次判定规则为 `score >= 80`：

- 达标：`passed: true`、`is_degraded: false`。
- 未达标：`passed: false`、`is_degraded: true`。
- 请求未完成或评分无效：分数和两个判定字段均为 `null`，不冒充通过或降智。

暂停/恢复仍使用原有按组账号状态机；新的分数会先转换为 success/degraded，再进入现有流程。迁移清除旧视觉判定的连续失败次数，保留已拥有的暂停状态等待新检测恢复。

每组最多保留 5 条历史，最新结果和历史在同一事务中保存。历史 HTML 按需读取，继续校验监控配置、允许分组、检测开关和账号归属。旧记录保留现有 HTML，不补造分数。

界面以时间胶囊显示最近 5 条；弹窗只展示判定、分数、检测模型和缩放 HTML。HTML 继续使用净化后的 sandbox iframe，禁止脚本和外部网络。

参考范围：

- `kiss-kedaya/sub2api` 固定提交 `ea88578d42f26b52241eb62b1b5ae0eed435c2dc`：当前质量检测已使用独立能力审计；`backend/internal/chanshui/policy.go` 提供可配置总分阈值，默认策略不是固定的 80 分。其历史胶囊组件位于 `frontend/src/components/user/monitor/ChannelMonitorV3QualityHistory.vue`。
- `https://tihu.app/rules/`：tihu 本身采用渲染图评分，疑似降智告警基于历史分布。本实现借用评分维度与权重，采用用户要求的源码输入及本地单次阈值，不实现 tihu 的七天历史基线告警。

没有执行真实付费模型调用、数据库迁移、测试、构建或发布。
