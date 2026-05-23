import type { ReactNode } from "react";
import type { UseQueryResult } from "@tanstack/react-query";

export type AdminScope = "self" | "all";

export interface EntityListParams {
  search?: string;
  scope?: AdminScope;
  page_size: number;
}

export interface EntityListResult<T> {
  data: T[];
}

export interface EntityAdapter<T = unknown> {
  /** entity 标识，作为 i18n key 前缀和 placeholder fallback。 */
  name: string;
  /** 列表 query。adapter 内部按 scope 决定 endpoint。 */
  useList(params: EntityListParams): UseQueryResult<EntityListResult<T> | undefined>;
  /** 已选 value 但 list 未加载时回显 label 用。返回 undefined 表示 entity 不需要单 item lookup。 */
  useOne(id: string, opts?: { scope?: AdminScope }): UseQueryResult<T | undefined>;
  /** 把 item 转为 value 字符串（默认 String(item.id)）。 */
  getValue(item: T): string;
  /** 把 item 转为可见 label。 */
  getLabel(item: T): string;
  /** 可选：item 富 UI 渲染（badge / icon / 副标题）。默认仅渲染 label。 */
  renderItem?(item: T): ReactNode;
  /** 是否暴露 admin scope toggle（仅 admin user 看到）。默认 false。 */
  supportsAdminScope?: boolean;
}
