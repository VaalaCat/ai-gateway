"use client";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useTranslations } from "next-intl";
import type { OpenAPIImportDraft } from "./openapi-import-state";

function publicURL(slug: string, path: string) {
	return `/v1/api/${slug}${path === "/" ? "" : path}`;
}

export function OpenAPIPreviewStep({ draft, disabled, onSelectedServerChange, onUpstreamBaseURLChange }: {
	draft: OpenAPIImportDraft;
	disabled: boolean;
	onSelectedServerChange: (server: number) => void;
	onUpstreamBaseURLChange: (value: string) => void;
}) {
	const t = useTranslations("apiServices");
	const { preview } = draft;
	return (
		<section
			className="flex flex-col gap-6"
			aria-labelledby="openapi-preview-step-title"
			data-disabled={disabled || undefined}
		>
			<div>
				<h2 id="openapi-preview-step-title" className="text-xl font-semibold tracking-tight">
					{t("openAPIPreview")}
				</h2>
				<p className="text-sm text-muted-foreground">{t("openAPIPreviewDescription")}</p>
			</div>
			<FieldSet>
				<FieldLegend>{t("openAPIService")}</FieldLegend>
				<FieldGroup>
					<Field>
						<FieldLabel>{t("name")}</FieldLabel>
						<FieldContent>
							<span className="font-medium">{preview.service.name}</span>
							<FieldDescription>{preview.service.description || preview.service.slug}</FieldDescription>
						</FieldContent>
					</Field>
					<Field>
						<FieldLabel>{t("slug")}</FieldLabel>
						<code className="text-sm font-semibold">{preview.service.slug}</code>
					</Field>
				</FieldGroup>
			</FieldSet>
			<FieldSet>
				<FieldLegend>{t("openAPIServers")}</FieldLegend>
				{preview.servers.length === 0 ? (
					<Field>
						<FieldLabel htmlFor="openapi-upstream-base-url">{t("upstreamBaseURL")}</FieldLabel>
						<Input
							id="openapi-upstream-base-url"
							value={draft.upstreamBaseURL}
							disabled={disabled}
							onChange={(event) => onUpstreamBaseURLChange(event.target.value)}
							placeholder="https://upstream.example.com"
						/>
						<FieldDescription>{t("openAPINoServersDescription")}</FieldDescription>
					</Field>
				) : (
					<RadioGroup
						value={draft.selectedServer === undefined ? "" : String(draft.selectedServer)}
						disabled={disabled}
						onValueChange={(value) => onSelectedServerChange(Number(value))}
					>
						{preview.servers.map((server) => (
							<Field key={server.index} orientation="horizontal">
								<RadioGroupItem
									value={String(server.index)}
									id={`openapi-server-${server.index}`}
									aria-label={server.url}
								/>
								<FieldContent>
									<FieldLabel htmlFor={`openapi-server-${server.index}`}>{server.url}</FieldLabel>
									{server.description ? <FieldDescription>{server.description}</FieldDescription> : null}
								</FieldContent>
							</Field>
						))}
					</RadioGroup>
				)}
			</FieldSet>
			<FieldSet>
				<FieldLegend>{t("openAPIRoutes")}</FieldLegend>
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>{t("route")}</TableHead>
							<TableHead>{t("paths")}</TableHead>
							<TableHead>{t("methods")}</TableHead>
							<TableHead>{t("gatewayURL")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{preview.routes.map((route) => (
							<TableRow key={route.slug || "root"}>
								<TableCell className="font-medium">
									{route.slug === "" ? t("rootRoute") : route.display_name}
								</TableCell>
								<TableCell>{route.paths.join(", ")}</TableCell>
								<TableCell>{route.allowed_methods.join(", ")}</TableCell>
								<TableCell className="font-mono text-xs">
									{Object.entries(route.public_paths).map(([path, publicPath]) => (
										<div key={path}>{publicURL(preview.service.slug, publicPath)}</div>
									))}
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</FieldSet>
			{preview.problems.length > 0 ? (
				<Alert>
					<AlertTitle>{t("openAPIWarnings")}</AlertTitle>
					<AlertDescription>
						{preview.problems.map((problem) => (
							<p key={`${problem.path}:${problem.code}`}>{problem.message}</p>
						))}
					</AlertDescription>
				</Alert>
			) : null}
		</section>
	);
}
