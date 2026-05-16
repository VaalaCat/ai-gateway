"use client";

import { RoutingForm } from "@/components/model-routing/routing-form";

export default function Page() {
  return <RoutingForm mode={{ kind: "new" }} apiMode="user" />;
}
