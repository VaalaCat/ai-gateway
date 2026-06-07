import { toast } from "sonner";

/**
 * Copy text to the clipboard in BOTH secure and insecure contexts.
 *
 * `navigator.clipboard` is only injected in secure contexts (HTTPS or
 * localhost/127.0.0.1). On plain http:// — e.g. a self-hosted gateway reached
 * by LAN IP — it is `undefined`, so reading `.writeText` throws synchronously.
 * In that case we skip the async Clipboard API entirely and fall back to a
 * hidden <textarea> + document.execCommand("copy").
 *
 * The fallback must run synchronously inside the user-gesture call stack for
 * execCommand to be honored, so we only `await` when the Clipboard API exists.
 *
 * @returns true if the copy succeeded.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Reached only in a secure context where writeText() rejected (permission
      // denied, transient failure). Intentionally fall through to the legacy
      // textarea path as a best-effort retry — legacyCopy returns false if it
      // also fails, so the caller still gets an honest success/failure result.
    }
  }
  return legacyCopy(text);
}

function legacyCopy(text: string): boolean {
  if (typeof document === "undefined") return false;
  const previouslyFocused = document.activeElement as HTMLElement | null;
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.top = "-9999px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  let ok = false;
  try {
    textarea.select();
    textarea.setSelectionRange(0, text.length);
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  } finally {
    document.body.removeChild(textarea);
    previouslyFocused?.focus?.();
  }
  return ok;
}

/**
 * Copy text and show a sonner toast for success/failure. i18n strings are
 * supplied by the caller (which holds the useTranslations hook), so this stays
 * callable from anywhere.
 *
 * @returns true if the copy succeeded.
 */
export async function copyTextWithFeedback(
  text: string,
  msg: { success: string; error: string },
): Promise<boolean> {
  const ok = await copyToClipboard(text);
  if (ok) toast.success(msg.success);
  else toast.error(msg.error);
  return ok;
}
