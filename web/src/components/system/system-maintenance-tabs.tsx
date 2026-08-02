"use client";

import type { ReactNode } from "react";
import { useTranslations } from "next-intl";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Database, Gauge, KeyRound, Route, ShieldCheck } from "lucide-react";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

interface SystemMaintenanceTabsProps {
  overview: ReactNode;
  requestPath: ReactNode;
  policyBilling: ReactNode;
  byok: ReactNode;
  dataMaintenance: ReactNode;
}

const tabs = [
  { value: "overview", label: "overview", icon: Gauge },
  { value: "request-path", label: "requestPath", icon: Route },
  { value: "policy-billing", label: "policyBilling", icon: ShieldCheck },
  { value: "byok", label: "byok", icon: KeyRound },
  { value: "data-maintenance", label: "dataMaintenance", icon: Database },
] as const;

type MaintenanceSection = (typeof tabs)[number]["value"];

function getMaintenanceSection(value: string | null): MaintenanceSection {
  return tabs.some((tab) => tab.value === value) ? value as MaintenanceSection : "overview";
}

export function SystemMaintenanceTabs({
  overview,
  requestPath,
  policyBilling,
  byok,
  dataMaintenance,
}: SystemMaintenanceTabsProps) {
  const t = useTranslations("system");
  const pathname = usePathname();
  const router = useRouter();
  const searchParams = useSearchParams();
  const section = getMaintenanceSection(searchParams.get("section"));
  const contents = {
    overview,
    "request-path": requestPath,
    "policy-billing": policyBilling,
    byok,
    "data-maintenance": dataMaintenance,
  } satisfies Record<(typeof tabs)[number]["value"], ReactNode>;

  const handleSectionChange = (nextSection: string) => {
    const params = new URLSearchParams(searchParams.toString());
    params.set("section", getMaintenanceSection(nextSection));
    router.replace(`${pathname}?${params.toString()}`, { scroll: false });
  };

  return (
    <Tabs value={section} onValueChange={handleSectionChange} className="min-w-0">
      <div className="min-w-0 overflow-x-auto">
        <TabsList
          aria-label={t("tabs.label")}
          className="w-max min-w-full justify-start md:grid md:grid-cols-5"
        >
          {tabs.map(({ value, label, icon: Icon }) => (
            <TabsTrigger
              key={value}
              value={value}
              data-testid={`system-maintenance-tab-${value}`}
              className="flex-none px-3 md:flex-1"
              onFocus={(event) => event.currentTarget.scrollIntoView({
                behavior: "smooth",
                block: "nearest",
                inline: "center",
              })}
            >
              <Icon data-icon="inline-start" />
              {t(`tabs.${label}`)}
            </TabsTrigger>
          ))}
        </TabsList>
      </div>

      {tabs.map(({ value }) => (
        <TabsContent
          key={value}
          value={value}
          forceMount
          className="min-w-0 data-[state=inactive]:hidden"
        >
          {contents[value]}
        </TabsContent>
      ))}
    </Tabs>
  );
}
