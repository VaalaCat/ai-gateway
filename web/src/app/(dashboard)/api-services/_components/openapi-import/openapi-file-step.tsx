"use client";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useTranslations } from "next-intl";

export function OpenAPIFileStep({ fileName, error, loading, disabled, onFileChange, onPreview }: {
	fileName?: string;
	error?: string;
	loading: boolean;
	disabled: boolean;
	onFileChange: (file: File | undefined) => void;
	onPreview: () => void;
}) {
	const t = useTranslations("apiServices");
	return (
		<section
			className="flex flex-col gap-4"
			aria-labelledby="openapi-file-step-title"
			data-disabled={disabled || undefined}
		>
			<div>
				<h2 id="openapi-file-step-title" className="text-xl font-semibold tracking-tight">
					{t("openAPIJSON")}
				</h2>
				<p className="text-sm text-muted-foreground">{t("openAPIFileStepDescription")}</p>
			</div>
			<FieldGroup>
				<Field>
					<FieldLabel htmlFor="openapi-file">{t("openAPIFile")}</FieldLabel>
					<Input
						id="openapi-file"
						className="min-h-10"
						type="file"
						accept=".json,application/json"
						disabled={disabled}
						onChange={(event) => onFileChange(event.target.files?.[0])}
					/>
					<FieldDescription>{fileName ?? t("openAPIFileDescription")}</FieldDescription>
				</Field>
			</FieldGroup>
			{error ? (
				<Alert variant="destructive">
					<AlertTitle>{t("openAPIFileError")}</AlertTitle>
					<AlertDescription>{error}</AlertDescription>
				</Alert>
			) : null}
			<div>
				<Button type="button" size="lg" onClick={onPreview} disabled={disabled || !fileName || loading}>
					{loading ? t("previewingOpenAPI") : t("previewOpenAPI")}
				</Button>
			</div>
		</section>
	);
}
