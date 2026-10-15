const fmtCache = new Map<string, Intl.DateTimeFormat>();

/** HH:MM:SS no fuso da empresa e no idioma do host. */
export function clock(iso: string, tz: string, lang: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const key = `${lang}|${tz}`;
  let f = fmtCache.get(key);
  if (!f) {
    try {
      f = new Intl.DateTimeFormat(lang || undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false, timeZone: tz || undefined });
    } catch {
      f = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
    }
    fmtCache.set(key, f);
  }
  return f.format(d);
}

/** Mesmo rótulo do host (kind técnico, sem tradução). */
export function kindLabel(kind: string): string {
  switch (kind) {
    case "tool_use":
      return "tool";
    case "tool_result":
      return "tool.result";
  }
  return kind;
}

/** Texto de uma linha recolhida: uma linha só, limitado. */
export function oneLine(text: string, max = 400): string {
  const s = text.replace(/\s+/g, " ").trim();
  return s.length > max ? s.slice(0, max) + "…" : s;
}

export function prettyPayload(payload: unknown, max = 20000): string {
  let s: string;
  try {
    s = JSON.stringify(payload, null, 2) ?? "";
  } catch {
    s = String(payload);
  }
  return s.length > max ? s.slice(0, max) + "\n…" : s;
}
