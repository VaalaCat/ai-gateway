"use client";

import { APIServiceFormEntryGuard } from "../_components/form-entry";
import { APIServiceForm } from "../_components/service-form";

export default function NewAPIServicePage() { return <APIServiceFormEntryGuard permission={{ kind: "create" }} titleKey="createService" descriptionKey="serviceFormDescription"><APIServiceForm mode={{ kind: "create" }} /></APIServiceFormEntryGuard>; }
