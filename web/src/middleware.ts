import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";
import { STORAGE_KEYS, ROLE } from "@/lib/constants";

const publicPaths = ["/login", "/register", "/oauth/bind", "/oauth/choose", "/oauth/success"];
const adminPaths = ["/users", "/channels", "/models", "/agents", "/agent-routes", "/system", "/token-templates", "/oauth-providers"];

function parseJWT(token: string) {
  try {
    const payload = token.split(".")[1];
    return JSON.parse(Buffer.from(payload, "base64").toString());
  } catch {
    return null;
  }
}

export function middleware(request: NextRequest) {
  const { pathname } = request.nextUrl;

  if (
    publicPaths.some((p) => pathname.startsWith(p)) ||
    pathname.startsWith("/_next") ||
    pathname.startsWith("/api") ||
    pathname === "/favicon.ico"
  ) {
    return NextResponse.next();
  }

  const token = request.cookies.get(STORAGE_KEYS.TOKEN)?.value;
  if (!token) {
    return NextResponse.redirect(new URL("/login", request.url));
  }

  const payload = parseJWT(token);
  if (!payload || payload.exp * 1000 < Date.now()) {
    const response = NextResponse.redirect(new URL("/login", request.url));
    response.cookies.delete(STORAGE_KEYS.TOKEN);
    return response;
  }

  if (adminPaths.some((p) => pathname.startsWith(p)) && payload.role !== ROLE.ADMIN) {
    return NextResponse.redirect(new URL("/dashboard", request.url));
  }

  return NextResponse.next();
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
