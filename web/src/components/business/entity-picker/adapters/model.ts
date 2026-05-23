import { useModels } from "@/lib/api/models";
import type { ModelConfig } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const modelAdapter: EntityAdapter<ModelConfig> = {
  name: "model",
  useList: ({ search, page_size }: EntityListParams) =>
    useModels({ search, page_size }) as ReturnType<EntityAdapter<ModelConfig>["useList"]>,
  // model 用 name 作为 value，不需要 single-item lookup（label = value）
  useOne: () =>
    ({
      data: undefined,
      isLoading: false,
      isSuccess: false,
      isError: false,
    }) as ReturnType<EntityAdapter<ModelConfig>["useOne"]>,
  getValue: (item) => item.model_name,
  getLabel: (item) => item.model_name,
};
