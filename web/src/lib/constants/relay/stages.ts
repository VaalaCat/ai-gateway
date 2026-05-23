/**
 * relay 流水线 stage 流程顺序,镜像 `internal/agent/relay/trace/stages.go` 的 Stage 枚举.
 * 给 StageDistributionBar 等 UI 按"请求从左到右流过这些 stage"的顺序排版,
 * 而不是按后端 count DESC 出错最多在前(后端只管聚合 + 排数,顺序由 UI 拍板).
 * `none` 在错误分布里不会出现,不列;`internal` 不属流水线,放末尾.
 */
export const RELAY_STAGE_ORDER: readonly string[] = [
  "inbound_decode",
  "outbound_encode",
  "upstream_dispatch",
  "upstream_status",
  "upstream_decode",
  "client_encode",
  "internal",
];
