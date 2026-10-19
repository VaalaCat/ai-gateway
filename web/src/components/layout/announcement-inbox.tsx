"use client";

import { useCallback, useEffect, useReducer, useState, useSyncExternalStore } from "react";
import { ArrowRight, Bell, Loader2, Pin } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useAuth } from "@/lib/auth";
import {
  useAnnouncements,
  usePublicAnnouncements,
  useMarkAnnouncementSeen,
  type Announcement,
  type ReceivedAnnouncement,
} from "@/lib/api/announcements";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";

const guestSeenKey = "announcement-seen-ids";
// Keep displayed versions across route remounts, including when persistence fails.
const sessions = new Map<string, { displayed: Set<string>; failed: Set<string> }>();
type AnnouncementVersion = Pick<Announcement, "id" | "revision">;

function versionKey({ id, revision }: AnnouncementVersion) {
  return `${id}:${revision}`;
}

function readGuestSeen(): string[] {
  const value: unknown = JSON.parse(localStorage.getItem(guestSeenKey) ?? "[]");
  if (!Array.isArray(value)) return [];
  return value.flatMap((entry: unknown) => {
    const key = typeof entry === "number" ? String(entry) : entry;
    if (typeof key !== "string" || !/^[1-9]\d*(?::[1-9]\d*)?$/.test(key)) return [];
    if (!key.split(":").every((part) => Number.isSafeInteger(Number(part)))) return [];
    // Existing guest receipts predate revisions and acknowledge revision 1 only.
    return [key.includes(":") ? key : `${key}:1`];
  });
}

function formatCreatedDate(date: Date, locale: string) {
  const currentYear = date.getFullYear() === new Date().getFullYear();
  if (locale.startsWith("zh")) {
    return `${currentYear ? "" : `${date.getFullYear()}年`}${date.getMonth() + 1}月${date.getDate()}日`;
  }
  return new Intl.DateTimeFormat(locale, {
    ...(currentYear ? {} : { year: "numeric" as const }),
    month: "short",
    day: "numeric",
  }).format(date);
}

function getSession(scope: string) {
  let session = sessions.get(scope);
  if (!session) {
    session = { displayed: new Set(), failed: new Set() };
    sessions.set(scope, session);
  }
  if (scope === "guest") {
    try {
      for (const id of readGuestSeen()) session.displayed.add(id);
    } catch {
      // Storage may be unavailable; the session still prevents repeated popups.
    }
  }
  return session;
}

function subscribeVisibility(listener: () => void) {
  document.addEventListener("visibilitychange", listener);
  return () => document.removeEventListener("visibilitychange", listener);
}

function pageIsVisible() {
  return document.visibilityState === "visible";
}

function AnnouncementDetail({
  announcement,
  onDisplayed,
}: {
  announcement: Announcement;
  onDisplayed: (version: AnnouncementVersion) => void;
}) {
  useEffect(() => {
    const record = () => {
      if (pageIsVisible()) onDisplayed({ id: announcement.id, revision: announcement.revision });
    };
    record();
    return subscribeVisibility(record);
  }, [announcement.id, announcement.revision, onDisplayed]);

  return (
    <p className="whitespace-pre-wrap break-words text-sm leading-relaxed [overflow-wrap:anywhere]">
      {announcement.content}
    </p>
  );
}

function Inbox({ userId }: { userId?: number }) {
  const t = useTranslations("announcementInbox");
  const locale = useLocale();
  const isGuest = userId === undefined;
  const received = useAnnouncements(userId);
  const publicFeed = usePublicAnnouncements(isGuest);
  const markSeen = useMarkAnnouncementSeen(userId);
  const { mutateAsync: markAnnouncementSeen } = markSeen;
  const query = isGuest ? publicFeed : received;
  const feed: ReceivedAnnouncement[] = isGuest
    ? (publicFeed.data ?? []).map((announcement) => ({ ...announcement, seen: false }))
    : received.data ?? [];
  const [session] = useState(() => getSession(isGuest ? "guest" : `user:${userId}`));
  const [, refresh] = useReducer((value: number) => value + 1, 0);
  const [view, setView] = useState<null | "list" | number>(null);
  const visible = useSyncExternalStore(subscribeVisibility, pageIsVisible, () => false);
  const detail = typeof view === "number" ? feed.find((item) => item.id === view) : undefined;
  const unread = feed.filter((item) => !item.seen && !session.displayed.has(versionKey(item)));
  const nextId = unread[0]?.id;

  const persistSeen = useCallback((version: AnnouncementVersion) => {
    const key = versionKey(version);
    const succeeded = () => {
      session.failed.delete(key);
      refresh();
    };
    const failed = () => {
      session.failed.add(key);
      refresh();
    };
    if (isGuest) {
      try {
        const ids = new Set([...readGuestSeen(), ...session.displayed]);
        localStorage.setItem(guestSeenKey, JSON.stringify([...ids]));
        succeeded();
      } catch {
        failed();
      }
    } else {
      void markAnnouncementSeen(version).then(succeeded, failed);
    }
  }, [isGuest, markAnnouncementSeen, session]);

  const recordDisplayed = useCallback((version: AnnouncementVersion) => {
    const key = versionKey(version);
    if (session.displayed.has(key)) return;
    session.displayed.add(key);
    refresh();
    persistSeen(version);
  }, [persistSeen, session]);


  useEffect(() => {
    if (!isGuest) return;
    const sync = (event: StorageEvent) => {
      if (event.key === guestSeenKey || event.key === null) {
        getSession("guest");
        refresh();
      }
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, [isGuest]);

  if (typeof view === "number" && !detail) {
    setView(null);
  } else if (view === null && visible && !query.isError && nextId !== undefined) {
    setView(nextId);
  }

  return (
    <Dialog open={view !== null} onOpenChange={(open) => { if (!open) setView(null); }}>
      <DialogTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="relative"
          aria-label={unread.length ? t("unreadCount", { count: unread.length }) : t("open")}
          onClick={() => setView("list")}
        >
          <Bell className="size-4" />
          {unread.length > 0 && (
            <span aria-hidden="true" className="absolute right-1 top-1 flex min-w-3.5 items-center justify-center rounded-full bg-destructive px-1 text-[10px] leading-3.5 text-white">
              {unread.length > 99 ? "99+" : unread.length}
            </span>
          )}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="break-words pr-6 [overflow-wrap:anywhere]">
            {detail?.title ?? t("title")}
          </DialogTitle>
          <DialogDescription>{t("description")}</DialogDescription>
        </DialogHeader>
        {detail ? (
          <>
            <AnnouncementDetail announcement={detail} onDisplayed={recordDisplayed} />
            {session.failed.has(versionKey(detail)) && (
              <div role="alert" className="space-y-2 text-sm text-destructive">
                <p>{t("seenError")}</p>
                <Button variant="outline" size="sm" disabled={markSeen.isPending} onClick={() => persistSeen({ id: detail.id, revision: detail.revision })}>
                  {t("retrySeen")}
                </Button>
              </div>
            )}
            <DialogFooter>
              <Button variant="outline" onClick={() => setView("list")}>{t("back")}</Button>
              <Button onClick={() => setView(null)}>{nextId === undefined ? t("close") : t("next")}</Button>
            </DialogFooter>
          </>
        ) : (
          <>
            {query.isLoading && (
              <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />{t("loading")}
              </p>
            )}
            {query.isError && (
              <div role="alert" className="flex items-center justify-between gap-3 text-sm text-destructive">
                <span>{t("error")}</span>
                <Button variant="outline" size="sm" onClick={() => { void query.refetch(); }}>{t("retry")}</Button>
              </div>
            )}
            {!query.isLoading && !query.isError && feed.length === 0 && (
              <p className="py-6 text-center text-sm text-muted-foreground">{t("empty")}</p>
            )}
            {feed.length > 0 && (
              <ul className="divide-y">
                {feed.map((item) => {
                  const created = new Date(item.created_at * 1000);
                  return (
                    <li key={item.id}>
                      <button type="button" className="flex w-full cursor-pointer items-center gap-3 rounded-sm px-2 py-3 text-left text-sm hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring" onClick={() => setView(item.id)}>
                        <span className="min-w-0 flex-1 space-y-1">
                          <span className="block break-words [overflow-wrap:anywhere]">{item.title}</span>
                          <span className="flex items-center gap-2 text-xs text-muted-foreground">
                            {item.pinned && <span className="inline-flex items-center gap-1"><Pin aria-hidden="true" className="size-3" />{t("pinned")}</span>}
                            <time dateTime={created.toISOString()}>{formatCreatedDate(created, locale)}</time>
                          </span>
                        </span>
                        {!item.seen && !session.displayed.has(versionKey(item)) && (
                          <span className="shrink-0 text-xs text-primary">{t("unread")}</span>
                        )}
                        <ArrowRight aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
            <DialogFooter><Button variant="outline" onClick={() => setView(null)}>{t("close")}</Button></DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function AnnouncementInbox({ guestsOnly = false }: { guestsOnly?: boolean }) {
  const { user, loading } = useAuth();
  if (loading || (guestsOnly && user) || (!guestsOnly && !user)) return null;
  return <Inbox key={user ? `user:${user.user_id}` : "guest"} userId={user?.user_id} />;
}
