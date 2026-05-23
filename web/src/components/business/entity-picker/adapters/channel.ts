import { useChannels, useChannel } from "@/lib/api/channels";
import type { Channel } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

export const channelAdapter: EntityAdapter<Channel> = {
  name: "channel",
  useList: ({ search, page_size }: EntityListParams) =>
    useChannels({ search, page_size }) as ReturnType<EntityAdapter<Channel>["useList"]>,
  useOne: (id) =>
    useChannel(id ? Number(id) : 0) as ReturnType<EntityAdapter<Channel>["useOne"]>,
  getValue: (item) => String(item.id),
  getLabel: (item) => item.name,
};
