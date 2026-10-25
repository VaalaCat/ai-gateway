"use client";

import { useCallback, useEffect, useReducer, useRef, useState, useSyncExternalStore } from "react";
import { ArrowRight, Bell, Loader2, Pin } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useAuth } from "@/lib/auth";
import {
  useAnnouncements,
  useAnnouncementDetail,
  useMarkAnnouncementSeen,
  usePrefetchAnnouncementDetail,
  usePrefetchAnnouncementPage,
  type Announcement,
  type ReceivedAnnouncementSummary,
} from "@/lib/api/announcements";
import { Button } from "@/components/ui/button";
import { DataTablePagination } from "@/components/data-table/pagination";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";

const guestSeenKey = "announcement-seen-ids";
// Keep displayed versions across route remounts, including when persistence fails.
const sessions = new Map<string, { displayed: Set<string>; failed: Set<string>; opened: Set<string>; synced: Set<string> }>();
type AnnouncementVersion = Pick<Announcement, "id" | "revision" | "show_for_guests">;

function versionKey({ id, revision }: AnnouncementVersion) {
  return `${id}:${revision}`;
}

function readGuestSeen(): string[] {
  const stored = localStorage.getItem(guestSeenKey) ?? "[]";
  let value: unknown;
  try {
    value = JSON.parse(stored);
  } catch {
    return [];
  }
  if (!Array.isArray(value)) return [];
  return value.filter((entry: unknown): entry is string =>
    typeof entry === "string" && /^[1-9]\d*:[1-9]\d*$/.test(entry)
    && entry.split(":").every((part) => Number.isSafeInteger(Number(part))),
  );
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
    session = { displayed: new Set(), failed: new Set(), opened: new Set(), synced: new Set() };
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
      if (pageIsVisible()) onDisplayed({ id: announcement.id, revision: announcement.revision, show_for_guests: announcement.show_for_guests });
    };
    record();
    return subscribeVisibility(record);
  }, [announcement.id, announcement.revision, announcement.show_for_guests, onDisplayed]);

  return (
    <p className="whitespace-pre-wrap break-words text-sm leading-relaxed [overflow-wrap:anywhere]">
      {announcement.content}
    </p>
  );
}

function AnnouncementLoading() {
  const t = useTranslations("announcementInbox");
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const timer = window.setTimeout(() => setVisible(true), 150);
    return () => window.clearTimeout(timer);
  }, []);
  if (!visible) return null;
  return (
    <div role="status" className="space-y-5">
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 aria-hidden="true" className="size-4 animate-spin" />{t("loading")}
      </p>
      <div aria-hidden="true" className="animate-pulse space-y-3">
        <div className="h-4 rounded bg-muted" />
        <div className="h-4 rounded bg-muted" />
        <div className="h-4 w-3/4 rounded bg-muted" />
        <div className="h-4 rounded bg-muted" />
        <div className="h-4 w-1/2 rounded bg-muted" />
      </div>
    </div>
  );
}

function Inbox({ userId }: { userId?: number }) {
  const t = useTranslations("announcementInbox");
  const locale = useLocale();
  const isGuest = userId === undefined;
  const [page, setPage] = useState(1);
  const query = useAnnouncements(userId, true, page);
  const totalPages = Math.max(1, Math.ceil(query.total / 10));
  const prefetchPage = usePrefetchAnnouncementPage(userId);
  const markSeen = useMarkAnnouncementSeen(userId);
  const { mutateAsync: markAnnouncementSeen } = markSeen;
  const feed: ReceivedAnnouncementSummary[] = query.loadedItems
    .map((item) => ({ ...item, seen: isGuest ? false : Boolean(item.seen) }));
  const [session] = useState(() => getSession(isGuest ? "guest" : `user:${userId}`));
  const [browserSeen] = useState(() => getSession("guest").displayed);
  const [, refresh] = useReducer((value: number) => value + 1, 0);
  const [view, setView] = useState<null | "list" | number>(null);
  const visible = useSyncExternalStore(subscribeVisibility, pageIsVisible, () => false);
  const selected = typeof view === "number" ? feed.find((item) => item.id === view) : undefined;
  const detailQuery = useAnnouncementDetail(selected?.id, userId);
  const detail = selected && !detailQuery.isFetching && !detailQuery.isError && detailQuery.data?.id === selected.id ? detailQuery.data : undefined;
  const isRead = (item: ReceivedAnnouncementSummary) => item.seen || session.displayed.has(versionKey(item))
    || (item.show_for_guests && browserSeen.has(versionKey(item)));
  const unread = feed.filter((item) => !isRead(item));
  const nextItem = unread.find((item) => !session.opened.has(versionKey(item)));
  const currentPage: ReceivedAnnouncementSummary[] = (query.data?.items ?? [])
    .map((item) => ({ ...item, seen: isGuest ? false : Boolean(item.seen) }));

  const persistSeen = useCallback(async (version: AnnouncementVersion, syncAccount = !isGuest) => {
    const key = versionKey(version);
    // Reconciliation gets one automatic attempt; failures require an explicit retry.
    session.synced.add(key);
    let saved = true;
    if (isGuest || version.show_for_guests) {
      browserSeen.add(key);
      try {
        const ids = new Set([...readGuestSeen(), ...browserSeen]);
        localStorage.setItem(guestSeenKey, JSON.stringify([...ids]));
      } catch {
        saved = false;
      }
    }
    if (syncAccount) {
      try {
        await markAnnouncementSeen({ id: version.id, revision: version.revision });
      } catch {
        saved = false;
      }
    }
    if (saved) session.failed.delete(key);
    else session.failed.add(key);
    refresh();
  }, [browserSeen, isGuest, markAnnouncementSeen, session]);

  const recordDisplayed = useCallback((version: AnnouncementVersion) => {
    const key = versionKey(version);
    if (session.displayed.has(key)) return;
    session.displayed.add(key);
    refresh();
    void persistSeen(version);
  }, [persistSeen, session]);

  useEffect(() => {
    if (isGuest) return;
    for (const item of feed) {
      const key = versionKey(item);
      if (!item.show_for_guests || session.synced.has(key)) continue;
      if (item.seen !== browserSeen.has(key)) {
        void persistSeen(item, !item.seen);
      }
    }
  }, [browserSeen, feed, isGuest, persistSeen, session]);

  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if (event.key === guestSeenKey || event.key === null) {
        getSession("guest");
        refresh();
      }
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);

  const prefetchDetail = usePrefetchAnnouncementDetail(userId);
  const prefetchTimer = useRef<number | undefined>(undefined);
  const cancelPrefetch = useCallback(() => {
    window.clearTimeout(prefetchTimer.current);
    prefetchTimer.current = undefined;
  }, []);
  const schedulePrefetch = (id: number) => {
    cancelPrefetch();
    prefetchTimer.current = window.setTimeout(() => {
      prefetchTimer.current = undefined;
      void prefetchDetail(id);
    }, 150);
  };
  useEffect(() => cancelPrefetch, [cancelPrefetch, page, view]);
  const failedReceipts = feed.filter((item) => session.failed.has(versionKey(item)));

  if (typeof view === "number" && !selected) {
    setView(null);
  } else if (view === null && visible && !query.isError && nextItem) {
    session.opened.add(versionKey(nextItem));
    setView(nextItem.id);
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
            <span aria-hidden="true" className="absolute right-1.5 top-1.5 size-2 rounded-full bg-destructive" />
          )}
        </Button>
      </DialogTrigger>
      <DialogContent aria-describedby={undefined} className="h-[min(42rem,85dvh)] max-h-[85dvh] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden">
        <DialogHeader>
          <DialogTitle className="line-clamp-2 h-12 break-words pr-6 leading-6 [overflow-wrap:anywhere]">
            {detail?.title ?? selected?.title ?? t("title")}
          </DialogTitle>
        </DialogHeader>
        <div key={selected?.id ?? "list"} className="min-h-0 space-y-4 overflow-y-auto overscroll-contain pr-1">
          {(selected ? detailQuery.isLoading || detailQuery.isFetching : !query.data && query.isLoading) && (
            <AnnouncementLoading />
          )}
          {selected ? (
            <>
              {detailQuery.isError && (
                <div role="alert" className="space-y-2 text-sm text-destructive">
                  <p>{t("detailError")}</p>
                  <Button variant="outline" size="sm" onClick={() => { void detailQuery.refetch(); }}>{t("retry")}</Button>
                </div>
              )}
              {detail && <AnnouncementDetail announcement={detail} onDisplayed={recordDisplayed} />}
              {detail && session.failed.has(versionKey(detail)) && (
                <div role="alert" className="space-y-2 text-sm text-destructive">
                  <p>{t("seenError")}</p>
                  <Button variant="outline" size="sm" disabled={markSeen.isPending} onClick={() => { void persistSeen(detail); }}>
                    {t("retrySeen")}
                  </Button>
                </div>
              )}
            </>
          ) : (
            <>
              {failedReceipts.length > 0 && (
                <div role="alert" className="flex flex-wrap items-center justify-between gap-3 text-sm text-destructive">
                  <span>{t("seenError")}</span>
                  <Button variant="outline" size="sm" disabled={markSeen.isPending} onClick={() => {
                    for (const item of failedReceipts) void persistSeen(item, !isGuest && !item.seen);
                  }}>{t("retrySeen")}</Button>
                </div>
              )}
              {query.isError && (
                <div role="alert" className="flex items-center justify-between gap-3 text-sm text-destructive">
                  <span>{t("error")}</span>
                  <Button variant="outline" size="sm" disabled={query.isFetching} onClick={() => { void query.refetch(); }}>{t("retry")}</Button>
                </div>
              )}
              {!query.isLoading && !query.isError && currentPage.length === 0 && (
                <p className="py-6 text-center text-sm text-muted-foreground">{t("empty")}</p>
              )}
              {!query.isError && currentPage.length > 0 && (
                <ul className="divide-y">
                  {currentPage.map((item) => {
                    const created = new Date(item.created_at * 1000);
                    return (
                      <li key={item.id}>
                        <button
                          type="button"
                          className="flex w-full cursor-pointer items-center gap-3 rounded-sm px-2 py-3 text-left text-sm hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring"
                          onMouseEnter={() => schedulePrefetch(item.id)}
                          onMouseLeave={cancelPrefetch}
                          onFocus={() => schedulePrefetch(item.id)}
                          onBlur={cancelPrefetch}
                          onClick={() => { session.opened.add(versionKey(item)); setView(item.id); }}
                        >
                          <span className="min-w-0 flex-1 space-y-1">
                            <span className="block break-words [overflow-wrap:anywhere]">{item.title}</span>
                            <span className="flex items-center gap-2 text-xs text-muted-foreground">
                              {item.pinned && <span className="inline-flex items-center gap-1"><Pin aria-hidden="true" className="size-3" />{t("pinned")}</span>}
                              <time dateTime={created.toISOString()}>{formatCreatedDate(created, locale)}</time>
                            </span>
                          </span>
                          {!isRead(item) && (
                            <span className="shrink-0">
                              <span aria-hidden="true" className="block size-2 rounded-full bg-destructive" />
                              <span className="sr-only">{t("unread")}</span>
                            </span>
                          )}
                          <ArrowRight aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </>
          )}
        </div>
        <DialogFooter className="flex-row flex-wrap items-center">
          {selected ? (
            <>
              <Button variant="outline" onClick={() => setView("list")}>{t("back")}</Button>
              <Button onClick={() => {
                if (nextItem) session.opened.add(versionKey(nextItem));
                setView(nextItem?.id ?? null);
              }}>{nextItem === undefined ? t("close") : t("next")}</Button>
            </>
          ) : (
            <>
              <DataTablePagination
                page={query.data?.page ?? page}
                pageSize={10}
                pageCount={totalPages}
                showPageSize={false}
                disabled={query.isFetching}
                onPaginationChange={setPage}
                onPrefetchPage={prefetchPage}
              />
              <Button variant="outline" onClick={() => setView(null)}>{t("close")}</Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function AnnouncementInbox({ guestsOnly = false }: { guestsOnly?: boolean }) {
  const { user, loading } = useAuth();
  if (loading || (guestsOnly && user) || (!guestsOnly && !user)) return null;
  return <Inbox key={user ? `user:${user.user_id}` : "guest"} userId={user?.user_id} />;
}
