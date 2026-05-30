import { useModels } from "@/lib/api/models";
import type { ModelConfig } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

// model 不需要单项 lookup:它的 value 就是 model_name,用 labelForValue 同步回显。
const EMPTY_ONE = {
  data: undefined,
  isLoading: false,
  isSuccess: false,
  isError: false,
} as ReturnType<EntityAdapter<ModelConfig>["useOne"]>;

export const modelAdapter: EntityAdapter<ModelConfig> = {
  name: "model",
  useList: ({ search, page_size }: EntityListParams) =>
    useModels({ search, page_size }) as ReturnType<EntityAdapter<ModelConfig>["useList"]>,
  useOne: () => EMPTY_ONE,
  getValue: (item) => item.model_name,
  getLabel: (item) => item.model_name,
  labelForValue: (v) => v || undefined,
};
