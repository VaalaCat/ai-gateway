"use client";

import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  normalizeOpenAPIParameters,
  openAPIObject,
  resolveOpenAPIObject,
  type OpenAPIObject,
} from "./openapi-operation-contract";
import { buildOpenAPIInvocationURL, type OpenAPIOperation } from "./openapi-operation-selection";

function readableJSON(value: unknown) {
  return JSON.stringify(value, null, 2) ?? "{}";
}

function CodeBlock({ value }: { value: unknown }) {
  return <pre className="max-h-64 overflow-auto rounded-md border border-border bg-muted/50 p-3 font-mono text-xs leading-5">{readableJSON(value)}</pre>;
}

function Reference({ value }: { value?: string }) {
  return value ? <code className="break-all font-mono text-xs">{value}</code> : null;
}

function SchemaSummary({ schema, components }: { schema: unknown; components: OpenAPIObject }) {
  const t = useTranslations("apiCatalog");
  const resolved = resolveOpenAPIObject(schema, components);
  if (!resolved.reference) return <CodeBlock value={schema} />;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <Reference value={resolved.reference} />
      {resolved.resolved
        ? resolved.recursive
          ? <p className="text-xs text-muted-foreground">{t("openAPIReferenceRecursive")}</p>
          : <CodeBlock value={resolved.value} />
        : null}
    </div>
  );
}

function ParameterTable({ operation, components }: { operation: OpenAPIOperation; components: OpenAPIObject }) {
  const t = useTranslations("apiCatalog");
  const parameters = normalizeOpenAPIParameters(operation, components);
  if (parameters.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <h3 className="text-sm font-semibold">{t("parameters")}</h3>
      <Table>
        <TableHeader><TableRow><TableHead>{t("name")}</TableHead><TableHead>{t("location")}</TableHead><TableHead>{t("required")}</TableHead><TableHead>{t("schema")}</TableHead></TableRow></TableHeader>
        <TableBody>{parameters.map((parameter) => (
          <TableRow key={`${parameter.in}:${parameter.name}`}>
            <TableCell className="font-mono text-xs">{parameter.name}</TableCell>
            <TableCell><Badge variant="outline">{parameter.in === "unknown" ? "" : parameter.in}</Badge></TableCell>
            <TableCell>{parameter.required ? t("yes") : t("no")}</TableCell>
            <TableCell>
              {parameter.reference ? <Reference value={parameter.reference} /> : null}
              <SchemaSummary schema={parameter.schema} components={components} />
            </TableCell>
          </TableRow>
        ))}</TableBody>
      </Table>
    </section>
  );
}

function NamedExamples({ examples, components }: { examples: unknown; components: OpenAPIObject }) {
  const collection = openAPIObject(examples);
  if (!collection) return null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {Object.keys(collection).sort().map((name) => {
        const resolved = resolveOpenAPIObject(collection[name], components);
        const value = Object.hasOwn(resolved.value, "value") ? resolved.value.value : resolved.value;
        return (
          <section key={name} className="flex min-w-0 flex-col gap-2">
            <span className="text-xs font-medium">{name}</span>
            <Reference value={resolved.reference} />
            {resolved.resolved || !resolved.reference ? <CodeBlock value={value} /> : null}
          </section>
        );
      })}
    </div>
  );
}

function MediaContent({ content, components }: { content: unknown; components: OpenAPIObject }) {
  const types = openAPIObject(content);
  if (!types) return null;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {Object.keys(types).sort().map((type) => {
        const item = openAPIObject(types[type]) ?? {};
        return (
          <section key={type} className="flex min-w-0 flex-col gap-2">
            <Badge variant="outline" className="w-fit font-mono">{type}</Badge>
            {item.schema ? <SchemaSummary schema={item.schema} components={components} /> : null}
            {item.example !== undefined ? <CodeBlock value={item.example} /> : null}
            <NamedExamples examples={item.examples} components={components} />
          </section>
        );
      })}
    </div>
  );
}

function ResponseHeaders({ headers, components }: { headers: unknown; components: OpenAPIObject }) {
  const collection = openAPIObject(headers);
  if (!collection) return null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {Object.keys(collection).sort().map((name) => {
        const resolved = resolveOpenAPIObject(collection[name], components);
        return (
          <section key={name} className="flex min-w-0 flex-col gap-1 rounded-md border border-border p-2">
            <span className="font-mono text-xs">{name}</span>
            <Reference value={resolved.reference} />
            {typeof resolved.value.description === "string" ? <p className="text-xs text-muted-foreground">{resolved.value.description}</p> : null}
            {resolved.resolved || !resolved.reference ? <SchemaSummary schema={resolved.value.schema} components={components} /> : null}
          </section>
        );
      })}
    </div>
  );
}

function RequestBodyDocument({ value, components }: { value: unknown; components: OpenAPIObject }) {
  const resolved = resolveOpenAPIObject(value, components);
  if (!value) return null;
  return (
    <section className="flex min-w-0 flex-col gap-2">
      <Reference value={resolved.reference} />
      {resolved.resolved || !resolved.reference ? (
        <>
          {typeof resolved.value.description === "string" ? <p className="text-sm text-muted-foreground">{resolved.value.description}</p> : null}
          <MediaContent content={resolved.value.content} components={components} />
        </>
      ) : null}
    </section>
  );
}

function ResponsesDocument({ value, components }: { value: unknown; components: OpenAPIObject }) {
  const responses = openAPIObject(value) ?? {};
  return (
    <>
      {Object.entries(responses).map(([status, response]) => {
        const resolved = resolveOpenAPIObject(response, components);
        return (
          <section key={status} className="flex min-w-0 flex-col gap-2 rounded-md border border-border p-3">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline">{status}</Badge>
              {typeof resolved.value.description === "string" ? <span className="text-sm text-muted-foreground">{resolved.value.description}</span> : null}
            </div>
            <Reference value={resolved.reference} />
            {resolved.resolved || !resolved.reference ? (
              <>
                <ResponseHeaders headers={resolved.value.headers} components={components} />
                <MediaContent content={resolved.value.content} components={components} />
              </>
            ) : null}
          </section>
        );
      })}
    </>
  );
}

export interface OpenAPIOperationDocumentProps {
  origin: string;
  serviceSlug: string;
  operation: OpenAPIOperation;
  components?: OpenAPIObject;
}

export function OpenAPIOperationDocument({ origin, serviceSlug, operation, components = {} }: OpenAPIOperationDocumentProps) {
  const t = useTranslations("apiCatalog");
  const description = typeof operation.operation.description === "string" ? operation.operation.description
    : typeof operation.operation.summary === "string" ? operation.operation.summary : "";
  const url = buildOpenAPIInvocationURL(origin, serviceSlug, operation.routeSlug, operation.path, {});

  return (
    <Card data-testid="openapi-operation-document">
      <CardHeader className="gap-3">
        <div className="flex min-w-0 flex-wrap items-center gap-2"><Badge variant="secondary" className="font-mono">{operation.method}</Badge><CardTitle className="break-all font-mono text-base">{operation.path}</CardTitle></div>
        <div className="flex min-w-0 flex-col gap-1 rounded-md border border-border bg-muted/50 p-3"><span className="text-xs font-medium text-muted-foreground">{t("openAPIRequestURL")}</span><code className="break-all font-mono text-sm">{url}</code></div>
        <p className="text-sm text-muted-foreground">{description || t("noDescription")}</p>
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-5">
        <ParameterTable operation={operation} components={components} />
        {operation.operation.requestBody ? <section className="flex min-w-0 flex-col gap-2"><h3 className="text-sm font-semibold">{t("requestBody")}</h3><RequestBodyDocument value={operation.operation.requestBody} components={components} /></section> : null}
        <section className="flex min-w-0 flex-col gap-3"><h3 className="text-sm font-semibold">{t("responses")}</h3><ResponsesDocument value={operation.operation.responses} components={components} /></section>
      </CardContent>
    </Card>
  );
}
