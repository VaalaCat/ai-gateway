"use client";

import { useState } from "react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { Pencil, User as UserIcon, LogOut } from "lucide-react";

import { useAuth } from "@/lib/auth";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Badge } from "@/components/ui/badge";

import { CopyableText } from "@/components/business/copyable-text";
import { ProfileFormDialog } from "@/components/business/profile-form-dialog";
import { useProfile } from "@/lib/api/users";

export function UserMenu() {
  const { user, logout } = useAuth();
  const tAuth = useTranslations("auth");
  const tUsers = useTranslations("users");
  const tProfile = useTranslations("profile");

  const [editOpen, setEditOpen] = useState(false);
  const { data: profile } = useProfile();

  const displayName = profile?.display_name?.trim() || user?.display_name?.trim() || user?.username || "U";
  const avatarURL = profile?.avatar_url || user?.avatar_url;
  const email = profile?.email || "";
  const userId = profile?.id ?? user?.user_id;
  const role = profile?.role ?? user?.role;

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="size-8 rounded-full">
            <Avatar size="sm">
              {avatarURL && <AvatarImage src={avatarURL} alt={displayName} />}
              <AvatarFallback>{displayName.charAt(0).toUpperCase()}</AvatarFallback>
            </Avatar>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-56">
          <DropdownMenuLabel className="flex flex-col gap-1 py-2">
            <div className="flex items-center gap-2">
              <span className="font-medium">{displayName}</span>
              <Badge variant="secondary">
                {role === 2 ? tUsers("roleAdmin") : tUsers("roleUser")}
              </Badge>
            </div>
            {email && <span className="text-xs text-muted-foreground font-normal">{email}</span>}
            {userId != null && (
              <span className="text-xs">
                <CopyableText text={String(userId)} display={`#${userId}`} />
              </span>
            )}
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => setEditOpen(true)}>
            <Pencil className="mr-2 size-4" />
            {tProfile("editProfile")}
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link href="/profile">
              <UserIcon className="mr-2 size-4" />
              {tProfile("myProfile")}
            </Link>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={logout}>
            <LogOut className="mr-2 size-4" />
            {tAuth("logout")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {profile && (
        <ProfileFormDialog
          key={`menu-profile-${profile.id}-${editOpen}`}
          mode="self"
          open={editOpen}
          onOpenChange={setEditOpen}
          initial={{
            email: profile.email ?? "",
            display_name: profile.display_name ?? "",
            avatar_url: profile.avatar_url ?? "",
          }}
          fallbackInitial={profile.username}
        />
      )}
    </>
  );
}
