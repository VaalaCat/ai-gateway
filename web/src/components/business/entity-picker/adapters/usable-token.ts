import { createElement } from "react";
import { useTokens, useUsableTokenForAPIRoute } from "@/lib/api/tokens";
import { useAuth } from "@/lib/auth";
import type { Token } from "@/lib/types";
import type { EntityAdapter, EntityListParams } from "../types";

const validID = (id: number | undefined): id is number =>
	typeof id === "number" && Number.isSafeInteger(id) && id > 0;

export const usableTokenAdapter: EntityAdapter<Token> = {
  name: "usable-token",
	useList: ({ search, scope, page_size, ownerUserId, apiServiceId, apiRouteId, enabled = true }: EntityListParams) => {
		const { user } = useAuth();
		const effectiveUserID = ownerUserId ?? (scope === "self" ? user?.user_id : undefined);
		const hasAPIScope = apiServiceId !== undefined || apiRouteId !== undefined;
		const validAPIScope = validID(apiServiceId) && validID(apiRouteId);
		return useTokens(
			{
        search,
        page_size,
        usable_only: true,
				...(effectiveUserID ? { user_id: effectiveUserID } : {}),
				...(hasAPIScope ? { api_service_id: apiServiceId ?? 0, api_route_id: apiRouteId ?? 0 } : {}),
			},
			{
        enabled: enabled && (!hasAPIScope || validAPIScope),
        cacheScope: ["viewer", user?.user_id ?? 0, "api-route", apiServiceId ?? 0, apiRouteId ?? 0],
      },
    ) as ReturnType<EntityAdapter<Token>["useList"]>;
  },
	useOne: (id, { scope, ownerUserId, apiServiceId, apiRouteId } = {}) => {
		const { user } = useAuth();
		const tokenID = id ? Number(id) : 0;
		const hasAPIScope = apiServiceId !== undefined || apiRouteId !== undefined;
		const effectiveUserID = ownerUserId ?? (scope === "self" ? user?.user_id : undefined);
		const scoped = useUsableTokenForAPIRoute({
      viewerUserID: user?.user_id ?? 0,
      ownerUserID: ownerUserId,
      apiServiceID: apiServiceId ?? 0,
      apiRouteID: apiRouteId ?? 0,
			tokenID,
		});
		const directQuery = useTokens(
			{
				token_id: tokenID,
				page: 1,
				page_size: 1,
				usable_only: true,
				...(effectiveUserID !== undefined ? { user_id: effectiveUserID } : {}),
			},
			{
				enabled: !hasAPIScope && validID(tokenID),
				cacheScope: ["viewer", user?.user_id ?? 0, "selected", tokenID, "owner", effectiveUserID ?? 0],
			},
		);
		const direct = {
			...directQuery,
			data: directQuery.data?.data.find((item) => (
				item.id === tokenID
				&& (effectiveUserID === undefined || item.user_id === effectiveUserID)
			)),
		};
		return (hasAPIScope ? scoped : direct) as ReturnType<EntityAdapter<Token>["useOne"]>;
	},
	getValue: (item) => String(item.id),
	getLabel: (item) => `${item.name} · ${item.owner_username}`,
	renderItem: (item) => createElement(
		"div",
		{ className: "flex min-w-0 flex-col" },
		createElement("span", { className: "truncate" }, item.name),
		createElement("span", { className: "truncate text-xs text-muted-foreground" }, item.owner_username),
	),
	supportsAdminScope: true,
};
