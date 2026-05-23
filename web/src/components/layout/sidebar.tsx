"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import {
  Activity,
  Bot,
  Brain,
  ChevronRight,
  FileKey,
  Key,
  KeyRound,
  LayoutDashboard,
  MessageSquare,
  Network,
  Route,
  ScrollText,
  Server,
  Wallet,
  UserCircle,
  Users,
  Users2,
  Wrench,
} from "lucide-react";
import { useAuth } from "@/lib/auth";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarSeparator,
} from "@/components/ui/sidebar";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { useSidebarSection } from "@/hooks/use-sidebar-section";

type LucideIcon = React.ComponentType<{ className?: string }>;
type Item = { label: string; icon: LucideIcon; href: string };
type Section = {
  key: string;
  label: string;
  defaultOpen: boolean;
  items: Item[];
};

function renderItem(item: Item, pathname: string) {
  return (
    <SidebarMenuItem key={item.href}>
      <SidebarMenuButton
        asChild
        isActive={pathname === item.href}
        tooltip={item.label}
      >
        <Link href={item.href}>
          <item.icon />
          <span>{item.label}</span>
        </Link>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

function AdminSection({
  section,
  pathname,
}: {
  section: Section;
  pathname: string;
}) {
  const [open, setOpen] = useSidebarSection(section.key, section.defaultOpen);
  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className="group/collapsible"
    >
      <SidebarGroup>
        <SidebarGroupLabel asChild>
          <CollapsibleTrigger className="flex w-full items-center">
            {section.label}
            <ChevronRight className="ml-auto transition-transform group-data-[state=open]/collapsible:rotate-90" />
          </CollapsibleTrigger>
        </SidebarGroupLabel>
        <CollapsibleContent>
          <SidebarGroupContent>
            <SidebarMenu>
              {section.items.map((it) => renderItem(it, pathname))}
            </SidebarMenu>
          </SidebarGroupContent>
        </CollapsibleContent>
      </SidebarGroup>
    </Collapsible>
  );
}

export function AppSidebar() {
  const pathname = usePathname();
  const t = useTranslations("nav");
  const { isAdmin } = useAuth();

  const userItems: Item[] = [
    { label: t("dashboard"), icon: LayoutDashboard, href: "/dashboard" },
    { label: t("tokens"), icon: Key, href: "/tokens" },
    { label: t("logs"), icon: ScrollText, href: "/logs" },
    { label: t("billing"), icon: Wallet, href: "/billing" },
    { label: t("playground"), icon: MessageSquare, href: "/playground" },
    { label: t("byok"), icon: KeyRound, href: "/byok" },
    { label: t("myModelRoutings"), icon: Network, href: "/profile/model-routings" },
  ];

  const adminSections: Section[] = isAdmin
    ? [
        {
          key: "access",
          label: t("section.access"),
          defaultOpen: true,
          items: [
            { label: t("users"), icon: Users, href: "/users" },
            { label: t("userGroups"), icon: Users2, href: "/groups" },
            {
              label: t("tokenTemplates"),
              icon: FileKey,
              href: "/token-templates",
            },
            {
              label: t("oauthProviders"),
              icon: KeyRound,
              href: "/oauth-providers",
            },
          ],
        },
        {
          key: "routing",
          label: t("section.routing"),
          defaultOpen: true,
          items: [
            { label: t("channels"), icon: Server, href: "/channels" },
            { label: t("byokAdmin"), icon: KeyRound, href: "/admin/byok" },
            { label: t("models"), icon: Brain, href: "/models" },
            { label: t("agents"), icon: Bot, href: "/agents" },
            { label: t("agentRoutes"), icon: Route, href: "/agent-routes" },
            { label: t("modelRoutings"), icon: Network, href: "/model-routings" },
          ],
        },
        {
          key: "ops",
          label: t("section.ops"),
          defaultOpen: false,
          items: [
            {
              label: t("monitoring"),
              icon: Activity,
              href: "/monitoring",
            },
            { label: t("maintenance"), icon: Wrench, href: "/system" },
          ],
        },
      ]
    : [];

  const bottomItems: Item[] = [
    { label: t("profile"), icon: UserCircle, href: "/profile" },
  ];

  return (
    <Sidebar>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" asChild>
              <Link href="/dashboard">
                <Bot className="size-5" />
                <span className="font-semibold">AI Gateway</span>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {userItems.map((it) => renderItem(it, pathname))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        {adminSections.map((s) => (
          <AdminSection key={s.key} section={s} pathname={pathname} />
        ))}
      </SidebarContent>
      <SidebarFooter>
        <SidebarSeparator />
        <SidebarMenu>
          {bottomItems.map((it) => renderItem(it, pathname))}
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  );
}
