import { STORAGE_KEYS, HTTP_HEADERS } from "@/lib/constants";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public body?: Record<string, unknown>
  ) {
    super(message);
    this.name = "ApiError";
  }
}

class ApiClient {
  private baseURL: string;

  constructor(baseURL: string = "") {
    this.baseURL = baseURL;
  }

  private getToken(): string | null {
    if (typeof window === "undefined") return null;
    return localStorage.getItem(STORAGE_KEYS.TOKEN);
  }

  private async requestResponse(path: string, options: RequestInit = {}): Promise<Response> {
    const token = this.getToken();
    const headers: Record<string, string> = {
      [HTTP_HEADERS.CONTENT_TYPE]: "application/json",
      ...(options.headers as Record<string, string>),
    };

    if (token) {
      headers[HTTP_HEADERS.AUTHORIZATION] = `Bearer ${token}`;
    }

    const res = await fetch(`${this.baseURL}${path}`, {
      ...options,
      headers,
    });

    if (res.status === 401) {
      const body = await res.json().catch(() => ({ error: res.statusText }));
      const isPublicAuthPath =
        path.includes("/login") ||
        path.includes("/oauth/register") ||
        path.includes("/oauth/bind");
      if (typeof window !== "undefined" && !isPublicAuthPath) {
        localStorage.removeItem(STORAGE_KEYS.TOKEN);
        document.cookie = `${STORAGE_KEYS.TOKEN}=; path=/; max-age=0`;
        window.location.href = "/login";
      }
      throw new ApiError(401, body.message || body.error || "Unauthorized", body);
    }

    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }));
      throw new ApiError(res.status, body.message || body.error || res.statusText, body);
    }

    return res;
  }

  async request<T>(path: string, options: RequestInit = {}): Promise<T> {
    const res = await this.requestResponse(path, options);

    return res.json();
  }

  get<T>(path: string): Promise<T> {
    return this.request<T>(path);
  }

  post<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>(path, {
      method: "POST",
      body: JSON.stringify(body),
    });
  }

  postRawJSON<T>(path: string, body: string): Promise<T> {
    return this.request<T>(path, { method: "POST", body });
  }

  async download(path: string, body: unknown): Promise<{ blob: Blob; filename: string | null }> {
    const res = await this.requestResponse(path, {
      method: "POST",
      body: JSON.stringify(body),
    });
    const disposition = res.headers.get("content-disposition") ?? "";
    const filename = /filename="?([^";]+)"?/i.exec(disposition)?.[1] ?? null;
    return { blob: await res.blob(), filename };
  }

  put<T>(path: string, body: unknown): Promise<T> {
    return this.request<T>(path, {
      method: "PUT",
      body: JSON.stringify(body),
    });
  }

  delete<T>(path: string): Promise<T> {
    return this.request<T>(path, { method: "DELETE" });
  }
}

export const api = new ApiClient("/api");

export function buildQuery<T extends object>(params: T): string {
  const sp = new URLSearchParams();
  Object.entries(params as Record<string, unknown>).forEach(([k, v]) => {
    if (typeof v === "boolean") {
      sp.set(k, String(v));
    } else if ((typeof v === "string" || typeof v === "number") && v !== "") {
      sp.set(k, String(v));
    }
  });
  const q = sp.toString();
  return q ? `?${q}` : "";
}
