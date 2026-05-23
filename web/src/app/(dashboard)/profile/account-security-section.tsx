"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { ChevronRight, Lock } from "lucide-react";

import { Card, CardContent } from "@/components/ui/card";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";

import { OAuthIdentityList } from "@/components/business/oauth-identity-list";
import { useChangePassword } from "@/lib/api/users";
import { formatErrorToast } from "@/lib/api/error-toast";

export function AccountSecuritySection() {
  const t = useTranslations("profile.security");
  const tu = useTranslations("users");

  const [open, setOpen] = useState(false);
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");

  const changePassword = useChangePassword();

  const handleChangePassword = async () => {
    if (newPassword !== confirmPassword) {
      toast.error(tu("passwordMismatch"));
      return;
    }
    try {
      await changePassword.mutateAsync({ old_password: oldPassword, new_password: newPassword });
      toast.success(tu("passwordChanged"));
      setOldPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (e) {
      toast.error(formatErrorToast(e, tu("wrongPassword")));
    }
  };

  const canSubmit = !!oldPassword && !!newPassword && !!confirmPassword && !changePassword.isPending;

  return (
    <Card>
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger asChild>
          <Button
            variant="ghost"
            className="w-full justify-between px-6 py-4 h-auto rounded-none"
          >
            <span className="flex items-center gap-2">
              <Lock className="size-5 text-muted-foreground" />
              <span className="text-lg font-semibold">{t("title")}</span>
              <span className="text-body font-normal text-muted-foreground hidden sm:inline">
                · {t("subtitle")}
              </span>
            </span>
            <ChevronRight
              className={`size-4 text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
            />
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <CardContent className="space-y-6 pt-2 pb-6">
            <section className="space-y-4">
              <h3 className="text-base font-semibold">{t("changePasswordHeading")}</h3>
              <div className="space-y-3 max-w-md">
                <div className="space-y-1.5">
                  <Label htmlFor="sec-old-pw">{tu("oldPassword")}</Label>
                  <Input
                    id="sec-old-pw"
                    type="password"
                    value={oldPassword}
                    onChange={(e) => setOldPassword(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="sec-new-pw">{tu("newPassword")}</Label>
                  <Input
                    id="sec-new-pw"
                    type="password"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="sec-confirm-pw">{tu("confirmPassword")}</Label>
                  <Input
                    id="sec-confirm-pw"
                    type="password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                  />
                </div>
                <Button onClick={handleChangePassword} disabled={!canSubmit}>
                  {tu("changePassword")}
                </Button>
              </div>
            </section>

            <Separator />

            <section className="space-y-4">
              <h3 className="text-base font-semibold">{t("connectedAccounts")}</h3>
              <OAuthIdentityList />
            </section>
          </CardContent>
        </CollapsibleContent>
      </Collapsible>
    </Card>
  );
}
