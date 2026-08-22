import type { OpenAPIImportInput, OpenAPIPreviewResponse } from "@/lib/api/api-services";

export interface OpenAPIImportDraft {
	fileName: string;
	document: unknown;
	preview: OpenAPIPreviewResponse;
	selectedServer?: number;
	upstreamBaseURL: string;
}

export function parseOpenAPIFile(file: File): Promise<{ fileName: string; document: unknown }> {
	return file.text().then((raw) => ({ fileName: file.name, document: JSON.parse(raw) as unknown }));
}

export function acceptsOpenAPIJSON(file: File) {
	return file.name.toLowerCase().endsWith(".json");
}

function limitedName(value: string, suffix: string) {
	return `${value || "OpenAPI"} ${suffix}`.slice(0, 64);
}

export function toOpenAPIImportInput(draft: OpenAPIImportDraft): OpenAPIImportInput {
	return {
		document: draft.document,
		slug: draft.preview.service.slug,
		choices: [],
		...(draft.selectedServer === undefined ? {} : { selected_server: draft.selectedServer }),
		backend_name: limitedName(draft.preview.service.name, "Target"),
		upstream: {
			name: limitedName(draft.preview.service.name, "Upstream"),
			...(draft.selectedServer === undefined ? { base_url: draft.upstreamBaseURL.trim() } : {}),
			weight: 1,
			priority: 0,
			auth_type: "none",
		},
		price_per_call: 0,
	};
}

export function canConfirmOpenAPIImport(draft: OpenAPIImportDraft | undefined) {
	if (!draft) return false;
	return draft.preview.servers.length === 0
		? draft.upstreamBaseURL.trim() !== ""
		: draft.selectedServer !== undefined;
}
