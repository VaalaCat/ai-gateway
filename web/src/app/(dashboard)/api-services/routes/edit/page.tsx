"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { APIServiceFormEntryGuard, FormPageSkeleton, InvalidFormEntry, RetryableFormEntryError, readPositiveID } from "../../_components/form-entry";
import { APIRouteForm } from "../../_components/route-form";
import { useAPIService } from "@/lib/api/api-services";

function EditRouteContent() { const params = useSearchParams(); const id = readPositiveID(params.get("id")); const serviceId = readPositiveID(params.get("service_id")); const service = useAPIService(serviceId ?? 0, { enabled: serviceId !== undefined }); if (!id || !serviceId) return <InvalidFormEntry titleKey="editRoute" subjectKey="routeNotFound" />; if (service.isLoading) return <FormPageSkeleton titleKey="editRoute" descriptionKey="routeFormDescription" />; if (service.error) return (typeof service.error === "object" && service.error !== null && "status" in service.error && (service.error as { status?: number }).status === 404) ? <InvalidFormEntry titleKey="editRoute" subjectKey="serviceNotFound" /> : <RetryableFormEntryError titleKey="editRoute" onRetry={() => void service.refetch()} />; return service.data?.id === serviceId ? <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId }} titleKey="editRoute" descriptionKey="routeFormDescription"><APIRouteForm mode={{ kind: "edit", id, serviceId, serviceSlug: service.data.slug }} /></APIServiceFormEntryGuard> : <InvalidFormEntry titleKey="editRoute" subjectKey="routeNotFound" />; }
export default function EditRoutePage() { return <Suspense fallback={<FormPageSkeleton titleKey="editRoute" descriptionKey="routeFormDescription" />}><EditRouteContent /></Suspense>; }
