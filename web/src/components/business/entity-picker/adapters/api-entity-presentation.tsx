import { useTranslations } from "next-intl";

import { StatusBadge } from "@/components/business/status-badge";
import type { APIBackend } from "@/lib/api/api-services";

export function renderAPIEntityItem(
  primary: string,
  secondary: string,
  status: number,
) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <span className="truncate">{primary}</span>
      <span className="truncate text-xs text-muted-foreground">{secondary}</span>
      <StatusBadge status={status} />
    </div>
  );
}

function APIBackendItem({ backend }: { backend: APIBackend }) {
	const t = useTranslations("apiServices");
	const summary = [
		t("routeCount", { count: backend.route_count }),
		t("upstreamCount", { count: backend.upstream_count }),
		backend.endpoint_hosts.join(", "),
	].filter(Boolean).join(" · ");
	return (
		<div className="flex min-w-0 items-center gap-2">
			<span className="truncate">{backend.name}</span>
			<span className="truncate text-xs text-muted-foreground">{summary}</span>
		</div>
	);
}

export function renderAPIBackendItem(backend: APIBackend) {
	return <APIBackendItem backend={backend} />;
}
