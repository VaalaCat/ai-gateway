"use client";

import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
} from "@/components/ui/breadcrumb";
import { LangSwitcher } from "@/components/layout/lang-switcher";
import { ThemeSwitcher } from "@/components/layout/theme-switcher";
import { UserMenu } from "@/components/layout/user-menu";

const pageKeys: Record<string, string> = {
  dashboard: "dashboard",
  users: "users",
  tokens: "tokens",
  channels: "channels",
  models: "models",
  agents: "agents",
  logs: "logs",
  playground: "playground",
  profile: "profile",
};

export function AppHeader() {
  const pathname = usePathname();
  const t = useTranslations("nav");

  const segment = pathname.split("/").filter(Boolean)[0] ?? "dashboard";
  const pageKey = pageKeys[segment] ?? "dashboard";

  return (
    <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mr-2 h-4" />
      <Breadcrumb>
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbPage>{t(pageKey)}</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>
      <div className="ml-auto flex items-center gap-1">
        <LangSwitcher />
        <ThemeSwitcher />
        <UserMenu />
      </div>
    </header>
  );
}
