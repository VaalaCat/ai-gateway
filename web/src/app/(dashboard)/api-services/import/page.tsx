"use client";

import { useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { LoaderCircle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { PageLayout } from "@/components/layout/page-layout";
import { useImportOpenAPI, useOpenAPIPreview } from "@/lib/api/api-services";

import { APIServiceFormEntryGuard } from "../_components/form-entry";
import { OpenAPIFileStep } from "../_components/openapi-import/openapi-file-step";
import { OpenAPIPreviewStep } from "../_components/openapi-import/openapi-preview-step";
import { acceptsOpenAPIJSON, canConfirmOpenAPIImport, parseOpenAPIFile, toOpenAPIImportInput, type OpenAPIImportDraft } from "../_components/openapi-import/openapi-import-state";
import { apiServiceErrorMessage } from "../api-service-error";

export function OpenAPIImportWorkspace() {
	const t = useTranslations("apiServices");
	const router = useRouter();
	const previewRequest = useOpenAPIPreview();
	const importRequest = useImportOpenAPI();
	const selectionRevision = useRef(0);
	const importPending = useRef(false);
	const [file, setFile] = useState<{ fileName: string; document: unknown }>();
	const [draft, setDraft] = useState<OpenAPIImportDraft>();
	const [fileError, setFileError] = useState<string>();
	const [importError, setImportError] = useState<string>();
	const [previewing, setPreviewing] = useState(false);
	const [submitting, setSubmitting] = useState(false);

	const chooseFile = (next: File | undefined) => {
		if (submitting || !next) return;
		if (!acceptsOpenAPIJSON(next)) { setFileError(t("openAPIFileJSONOnly")); return; }

		const revision = ++selectionRevision.current;
		setFile(undefined);
		setDraft(undefined);
		setImportError(undefined);
		setFileError(undefined);
		setPreviewing(false);
		void parseOpenAPIFile(next)
			.then((parsed) => {
				if (revision !== selectionRevision.current) return;
				setFile(parsed);
			})
			.catch(() => {
				if (revision === selectionRevision.current) setFileError(t("openAPIFileInvalid"));
			});
	};

	const preview = async () => {
		if (submitting || !file) return;
		const revision = selectionRevision.current;
		setDraft(undefined);
		setPreviewing(true);
		setFileError(undefined);
		try {
			const result = await previewRequest.mutateAsync(file.document);
			if (revision !== selectionRevision.current) return;
			setDraft({ ...file, preview: result, upstreamBaseURL: "" });
		} catch (reason) {
			if (revision === selectionRevision.current) {
				setFileError(apiServiceErrorMessage(t, reason, "openAPIPreviewFailed"));
			}
		} finally {
			if (revision === selectionRevision.current) setPreviewing(false);
		}
	};

	const confirm = async () => {
		if (importPending.current || !draft || !canConfirmOpenAPIImport(draft)) return;
		let committed = false;
		importPending.current = true;
		setSubmitting(true);
		setImportError(undefined);
		try {
			const result = await importRequest.mutateAsync(toOpenAPIImportInput(draft));
			committed = true;
			router.push(`/api-services/detail?id=${result.service_id}`);
		} catch (reason) {
			setImportError(apiServiceErrorMessage(t, reason, "openAPIImportFailed"));
		} finally {
			if (!committed) {
				importPending.current = false;
				setSubmitting(false);
			}
		}
	};

	return (
		<PageLayout
			title={t("importOpenAPI")}
			description={t("importOpenAPIDescription")}
			maxWidth="5xl"
			footer={(
				<>
					<Button
						type="button"
						size="lg"
						variant="outline"
						onClick={() => router.push("/api-services")}
						disabled={submitting}
					>
						{t("cancel")}
					</Button>
					<Button
						type="button"
						size="lg"
						onClick={() => void confirm()}
						disabled={submitting || !canConfirmOpenAPIImport(draft)}
					>
						{submitting ? (
							<>
								<LoaderCircle data-icon="inline-start" className="animate-spin" />
								{t("importingOpenAPI")}
							</>
						) : t("confirmImport")}
					</Button>
				</>
			)}
		>
			<div className="flex flex-col gap-8">
				<OpenAPIFileStep
					fileName={file?.fileName}
					error={fileError}
					loading={previewing}
					disabled={submitting}
					onFileChange={chooseFile}
					onPreview={() => void preview()}
				/>
				{draft ? (
					<OpenAPIPreviewStep
						draft={draft}
						disabled={submitting}
						onSelectedServerChange={(selectedServer) => setDraft((current) => (
							current ? { ...current, selectedServer } : current
						))}
						onUpstreamBaseURLChange={(upstreamBaseURL) => setDraft((current) => (
							current ? { ...current, upstreamBaseURL } : current
						))}
					/>
				) : null}
				{importError ? (
					<Alert variant="destructive">
						<AlertTitle>{t("openAPIImportFailed")}</AlertTitle>
						<AlertDescription>{importError}</AlertDescription>
					</Alert>
				) : null}
			</div>
		</PageLayout>
	);
}

export default function OpenAPIImportPage() {
	return (
		<APIServiceFormEntryGuard
			permission={{ kind: "create" }}
			titleKey="importOpenAPI"
			descriptionKey="importOpenAPIDescription"
		>
			<OpenAPIImportWorkspace />
		</APIServiceFormEntryGuard>
	);
}
