import type { EntityName } from "@/components/business/entity-picker/registry";

export interface FilterContext {
  isAdmin: boolean;
  /** 扩展点：page 可塞自定义 flag（如 hasOwnBYOK）。 */
  [key: string]: unknown;
}

type FilterDefBase = {
  /** 可选 label override。 */
  label?: string;
  /** 可见性条件。未设置 = 总是显示。 */
  visible?: (ctx: FilterContext) => boolean;
};

export type FilterDef =
  | (FilterDefBase & {
      kind: "time";
      /** 默认窗口天数（不写入 URL，仅影响初次显示）。 */
      defaultDays?: number;
      /** hour 粒度最大天数（默认 7，对齐 backend MaxHourRangeDays）。 */
      maxHourDays?: number;
      /** 是否暴露 day/hour 切换（默认 true）。 */
      showGran?: boolean;
    })
  | (FilterDefBase & {
      kind: "picker";
      entity: EntityName;
      /** 传给 EntityPicker 的 placeholder。 */
      placeholder?: string;
    })
  | (FilterDefBase & {
      kind: "enum";
      options: Array<{ value: string; label: string }>;
      /** 是否包含"全部"选项（value=""）；默认 true。 */
      includeAll?: boolean;
      placeholder?: string;
    })
  | (FilterDefBase & {
      kind: "text";
      placeholder?: string;
      /** 输入 debounce 毫秒；默认 300。 */
      debounceMs?: number;
    });

export type FilterSpec = Record<string, FilterDef>;

/** Filter 值字典。空值约定：undefined 表示未设置。
 *  time kind 的特殊键：spec key 必须叫 "time"，URL 写到 "start"/"end" 两个键。 */
export type FilterValues = Record<string, string | number | undefined>;
