import { Suspense } from "react";
import { AnnouncementInbox } from "@/components/layout/announcement-inbox";

export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <>
      <header className="absolute right-0 top-0 z-10 flex h-14 items-center px-4">
        <AnnouncementInbox guestsOnly />
      </header>
      <Suspense>{children}</Suspense>
    </>
  );
}
