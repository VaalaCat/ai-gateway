import type { ReactNode } from "react";

export type AdminScope = "self" | "all";

export interface EntityPickerQuery {
	apiServiceId?: number;
	apiRouteId?: number;
	disabled?: boolean;
}

export interface EntityListParams {
  search?: string;
  scope?: AdminScope;
  ownerUserId?: number;
  /** 仅返回指定 API Service 下的候选实体（例如 Route / Upstream）。 */
	apiServiceId?: number;
	/** 与 API Service 一起限制可 invoke Token 的目标 Route。 */
	apiRouteId?: number;
  page_size: number;
  /** Picker list queries stay mounted unconditionally and are enabled only while the popover is open. */
  enabled?: boolean;
}

export interface EntityListResult<T> {
  data: T[];
}

/** EntityPicker 实际消费的 React Query 状态；不向 adapter 暴露完整 observer 泛型。 */
export interface EntityQueryState<T> {
  data: T | undefined;
  isLoading: boolean;
  isError: boolean;
  error: Error | null;
  refetch: () => Promise<unknown>;
}

export interface EntityOneParams {
	scope?: AdminScope;
	ownerUserId?: number;
	apiServiceId?: number;
	apiRouteId?: number;
}

export interface EntityAdapter<T = unknown> {
  /** entity 标识，作为 i18n key 前缀和 placeholder fallback。 */
  name: string;
  /** 列表 query。adapter 内部按 scope 决定 endpoint。 */
  useList(params: EntityListParams): EntityQueryState<EntityListResult<T>>;
  /** 已选 value 但 list 未加载时回显 label 用。返回 undefined 表示 entity 不需要单 item lookup。 */
  useOne(
    id: string,
		opts?: EntityOneParams,
  ): EntityQueryState<T>;
  /** 把 item 转为 value 字符串（默认 String(item.id)）。 */
  getValue(item: T): string;
  /** 把 item 转为可见 label。 */
  getLabel(item: T): string;
  /** 可选：item 富 UI 渲染（badge / icon / 副标题）。默认仅渲染 label。 */
  renderItem?(item: T): ReactNode;
  /** 可选:value 本身即可读 label 时(如 model 的 model_name),无需 useOne 即可同步回显。 */
  labelForValue?(value: string): string | undefined;
  /** 是否暴露 admin scope toggle（仅 admin user 看到）。默认 false。 */
  supportsAdminScope?: boolean;
}
