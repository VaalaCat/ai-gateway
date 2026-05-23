import {
  useBYOKChannels,
  useBYOKChannel,
  useAdminBYOKChannels,
  useAdminBYOKChannel,
  type BYOKChannelDetail,
} from "@/lib/api/byok-channels";
import type { EntityAdapter, EntityListParams } from "../types";

export const byokChannelAdapter: EntityAdapter<BYOKChannelDetail> = {
  name: "byok-channel",
  useList: ({ search, scope, page_size }: EntityListParams) => {
    const isAdmin = scope === "all";
    const adminQ = useAdminBYOKChannels(
      { search, page_size },
      { enabled: isAdmin },
    );
    const selfQ = useBYOKChannels(
      { search, page_size },
      { enabled: !isAdmin },
    );
    return (isAdmin ? adminQ : selfQ) as ReturnType<EntityAdapter<BYOKChannelDetail>["useList"]>;
  },
  useOne: (id, opts) => {
    const isAdmin = opts?.scope === "all";
    // Call both unconditionally; gating is handled via enabled: !!id inside each hook
    const adminQ = useAdminBYOKChannel(id && isAdmin ? Number(id) : 0);
    const selfQ = useBYOKChannel(id && !isAdmin ? Number(id) : 0);
    return (isAdmin ? adminQ : selfQ) as ReturnType<EntityAdapter<BYOKChannelDetail>["useOne"]>;
  },
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
  supportsAdminScope: true,
};
