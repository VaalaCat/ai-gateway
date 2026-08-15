import {
	useAPIBackend,
	useAPIBackends,
	type APIBackend,
} from "@/lib/api/api-services";
import type { EntityAdapter, EntityListParams } from "../types";
import { renderAPIBackendItem } from "./api-entity-presentation";

const validID = (id: number | undefined): id is number =>
	typeof id === "number" && Number.isSafeInteger(id) && id > 0;

export const apiBackendAdapter: EntityAdapter<APIBackend> = {
	name: "api-backend",
	useList: ({ apiServiceId, search, page_size, enabled = true }: EntityListParams) => {
		const serviceID = validID(apiServiceId) ? apiServiceId : 0;
		return useAPIBackends(
			{ api_service_id: serviceID, search, page_size },
			{ enabled: enabled && validID(apiServiceId) },
		);
	},
	useOne: (id) => useAPIBackend(Number(id)),
	getValue: (item) => String(item.id),
	getLabel: (item) => item.name,
	renderItem: renderAPIBackendItem,
};
