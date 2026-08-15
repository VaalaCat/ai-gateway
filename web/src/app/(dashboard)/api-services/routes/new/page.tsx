"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { APIServiceFormEntryGuard, FormPageSkeleton, InvalidFormEntry, RetryableFormEntryError, readPositiveID } from "../../_components/form-entry";
import { APIRouteForm } from "../../_components/route-form";
import { useAPIService } from "@/lib/api/api-services";

function NewRouteContent() { const serviceId = readPositiveID(useSearchParams().get("service_id")); const service = useAPIService(serviceId ?? 0, { enabled: serviceId !== undefined }); if (!serviceId) return <InvalidFormEntry titleKey="createRoute" subjectKey="serviceNotFound" />; if (service.isLoading) return <FormPageSkeleton titleKey="createRoute" descriptionKey="routeFormDescription" />; if (service.error) return (typeof service.error === "object" && service.error !== null && "status" in service.error && (service.error as { status?: number }).status === 404) ? <InvalidFormEntry titleKey="createRoute" subjectKey="serviceNotFound" /> : <RetryableFormEntryError titleKey="createRoute" onRetry={() => void service.refetch()} />; return service.data?.id === serviceId ? <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId }} titleKey="createRoute" descriptionKey="routeFormDescription"><APIRouteForm mode={{ kind: "create", serviceId, serviceSlug: service.data.slug }} /></APIServiceFormEntryGuard> : <InvalidFormEntry titleKey="createRoute" subjectKey="serviceNotFound" />; }
export default function NewRoutePage() { return <Suspense fallback={<FormPageSkeleton titleKey="createRoute" descriptionKey="routeFormDescription" />}><NewRouteContent /></Suspense>; }
