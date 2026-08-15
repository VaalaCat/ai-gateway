"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { APIServiceFormEntryGuard, FormPageSkeleton, InvalidFormEntry, readPositiveID } from "../../_components/form-entry";
import { APIUpstreamForm } from "../../_components/upstream-form";
import { routeReturnContext } from "../../_components/route-return";

function NewUpstreamContent() { const params = useSearchParams(); const serviceId = readPositiveID(params.get("service_id")); const backendId = readPositiveID(params.get("backend_id")); const copyID = readPositiveID(params.get("copy_id")); const returnRoute = routeReturnContext(params); if (!serviceId) return <InvalidFormEntry titleKey="createUpstream" subjectKey="serviceNotFound" />; const titleKey = copyID ? "copyEndpoint" : "createUpstream"; return <APIServiceFormEntryGuard permission={{ kind: "manage", serviceId }} titleKey={titleKey} descriptionKey="upstreamFormDescription"><APIUpstreamForm mode={copyID ? { kind: "copy", id: copyID, serviceId, ...(returnRoute ? { returnRoute } : {}) } : { kind: "create", serviceId, ...(backendId ? { backendId } : {}), ...(returnRoute ? { returnRoute } : {}) }} /></APIServiceFormEntryGuard>; }
export default function NewUpstreamPage() { return <Suspense fallback={<FormPageSkeleton titleKey="createUpstream" descriptionKey="upstreamFormDescription" />}><NewUpstreamContent /></Suspense>; }
