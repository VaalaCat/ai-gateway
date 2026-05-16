"use client";

import { useTranslations } from "next-intl";
import { useStats, useStatsTrend } from "@/lib/api/stats";
import { useAuth } from "@/lib/auth";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Users,
  Key,
  Server,
  Brain,
  Bot,
  Wifi,
  ScrollText,
  DollarSign,
} from "lucide-react";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";

export default function DashboardPage() {
  const t = useTranslations("dashboard");
  const { isAdmin } = useAuth();
  const { data: stats, isLoading } = useStats();
  const { data: trendData } = useStatsTrend(30);

  const adminCards = [
    { key: "users", icon: Users, value: stats?.users },
    { key: "tokens", icon: Key, value: stats?.tokens },
    { key: "channels", icon: Server, value: stats?.channels },
    { key: "models", icon: Brain, value: stats?.models },
    { key: "agents", icon: Bot, value: stats?.agents },
    { key: "connectedAgents", icon: Wifi, value: stats?.connected_agents },
    { key: "usageLogs", icon: ScrollText, value: stats?.usage_logs },
    {
      key: "totalCost",
      icon: DollarSign,
      value: stats?.total_cost,
      isCost: true,
    },
  ];

  const userCards = [
    { key: "myTokens", icon: Key, value: stats?.tokens },
    { key: "myRequests", icon: ScrollText, value: stats?.usage_logs },
    {
      key: "myCost",
      icon: DollarSign,
      value: stats?.total_cost,
      isCost: true,
    },
  ];

  const cards = isAdmin ? adminCards : userCards;

  return (
    <div>
      <h1 className="text-2xl font-bold">{t("title")}</h1>
      <p className="text-muted-foreground mt-1">{t("description")}</p>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mt-6">
        {cards.map(({ key, icon: Icon, value, isCost }) => (
          <Card key={key}>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">
                {t(key)}
              </CardTitle>
              <Icon className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              {isLoading ? (
                <Skeleton className="h-8 w-20" />
              ) : (
                <div className="text-2xl font-bold">
                  {isCost
                    ? `$ ${((value ?? 0) / 100000).toFixed(4)}`
                    : (value ?? 0)}
                </div>
              )}
            </CardContent>
          </Card>
        ))}

        {!isAdmin && stats?.quota != null && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">{t("quota")}</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="text-2xl font-bold">
                {((stats.used_quota || 0) / 100000).toFixed(4)} / {(stats.quota / 100000).toFixed(4)}
              </div>
              <div className="mt-2 h-2 w-full rounded-full bg-secondary">
                <div
                  className="h-full rounded-full bg-primary"
                  style={{ width: `${Math.min(100, stats.quota > 0 ? ((stats.used_quota || 0) / stats.quota) * 100 : 0)}%` }}
                />
              </div>
            </CardContent>
          </Card>
        )}
      </div>

      {!isAdmin && trendData?.items && trendData.items.length > 0 && (
        <Card className="mt-6">
          <CardHeader>
            <CardTitle>{t("trend")}</CardTitle>
          </CardHeader>
          <CardContent>
            <ResponsiveContainer width="100%" height={300}>
              <LineChart data={trendData.items}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="date" />
                <YAxis />
                <Tooltip />
                <Line type="monotone" dataKey="requests" stroke="#8884d8" name={t("myRequests")} />
              </LineChart>
            </ResponsiveContainer>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
