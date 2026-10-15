/** Erro de API com a mensagem do problem+json do servidor (já no idioma do host quando possível). */
export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

/** Chamada JSON same-origin (cookie de sessão; CSRF pelo Sec-Fetch-Site do navegador). */
export async function requestJSON<R>(fetchImpl: FetchLike, url: string, init: RequestInit = {}): Promise<R> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (init.body) headers["Content-Type"] = "application/json";
  const res = await fetchImpl(url, { credentials: "same-origin", ...init, headers: { ...headers, ...(init.headers as Record<string, string> | undefined) } });
  const text = await res.text();
  let body: unknown = null;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    body = null;
  }
  if (!res.ok) {
    const p = (body ?? {}) as { detail?: string; title?: string; errors?: { message?: string }[] };
    const extra = p.errors?.[0]?.message;
    throw new ApiError(res.status, [p.detail || p.title || `HTTP ${res.status}`, extra].filter(Boolean).join(": "));
  }
  return body as R;
}
