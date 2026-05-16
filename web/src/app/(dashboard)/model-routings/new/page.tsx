"use client";

import { RoutingForm } from "@/components/model-routing/routing-form";

export default function NewPage() {
  return <RoutingForm mode={{ kind: "new" }} apiMode="admin" />;
}
