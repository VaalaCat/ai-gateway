# BYOK billing migration (2026-05-17)

`channel_daily_billings` 表新增两列 `private_channel_id` + `owner_type`，
唯一键从 `(date, channel_id)` 升级为 `(date, channel_id, private_channel_id)`
三列联合，以便 BYOK channel（`channel_id=0`, `private_channel_id>0`）和
admin channel（`channel_id>0`, `private_channel_id=0`）能共存于同一张表，
不再因为 BYOK 行都填 `channel_id=0` 而被聚合成一条脏行。

## 字段变更

| 字段                  | 类型     | 默认值     | 说明                                  |
| --------------------- | -------- | ---------- | ------------------------------------- |
| `private_channel_id`  | `uint`   | `0`        | BYOK 行 = 对应 `private_channels.id`；admin 行 = `0` |
| `owner_type`          | `string` | `'admin'`  | `"admin"` 或 `"private"`，与 `usage_logs.owner_type` 对齐 |

唯一索引：
- 删除（自动）：`idx_channel_daily_billing_date_channel` —— 由 `models/migrate.go` 的
  `dropLegacyChannelBillingIndex` 在启动时通过 `DROP INDEX IF EXISTS` 自动 drop，
  幂等可重复执行，新装部署无旧索引也安全。
- 新增（GORM AutoMigrate）：`idx_cdb_date_channel_pchan`

## SQLite（默认部署）

`AutoMigrate` 在启动时自动添加新列 + 创建新索引 + drop 旧索引。无需运维介入。
首次启动后旧索引即被清理；后续启动 `IF EXISTS` 子句兜底，重复执行无副作用。

## 历史数据回填

升级前已经积累的 BYOK `usage_logs`（`private_channel_id>0` /
`owner_type="private"`）对应的 daily rollup 行在旧表里会全部塞在
`channel_id=0` 那条脏行上，无法准确还原到每个 BYOK channel。
若需要重建，可以运行管理端的 daily rollup rebuild：

```sql
DELETE FROM channel_daily_billings WHERE channel_id = 0;
```

然后调用 `RebuildDailyRollups` API（或 SQL 自行实现），让 settler
按新逻辑从 `usage_logs` 重算。`usage_logs` 自 Task 12 起已经携带
`private_channel_id` + `owner_type`，回填路径无信息丢失。

如果不在意历史 BYOK rollup 精度，直接 DROP 该脏行即可：

```sql
DELETE FROM channel_daily_billings WHERE channel_id = 0 AND private_channel_id = 0;
```

## 唯一键的 NULL 行为

SQLite/MySQL/PostgreSQL 对 unique 索引中 `NULL` 列的处理不一致
（允许多个 NULL 共存）。我们用 `0`（而非 NULL）作为"没有"的占位值，
所以三种数据库行为统一：
- 同一天同一 admin channel 5 ⇒ 唯一行 `(date, 5, 0)`
- 同一天同一 BYOK pchan 7 ⇒ 唯一行 `(date, 0, 7)`
- 两者不冲突。
