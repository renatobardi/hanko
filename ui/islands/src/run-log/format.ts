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

export type Part = { text: string; match: boolean };

/**
 * Divide o texto em pedaços, marcando onde a busca casa (sem diferenciar caixa). Os offsets vêm de uma regex sobre o
 * texto ORIGINAL: baixar a caixa do texto inteiro muda o tamanho de algumas letras ("İ" vira "i" + ponto combinante)
 * e deslocaria todas as marcas depois dela.
 */
export function highlightParts(text: string, q: string): Part[] {
  const needle = q.trim();
  if (!needle) return [{ text, match: false }];
  const re = new RegExp(needle.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "giu");
  const out: Part[] = [];
  let from = 0;
  for (const m of text.matchAll(re)) {
    const i = m.index ?? 0;
    if (m[0] === "") continue;
    if (i > from) out.push({ text: text.slice(from, i), match: false });
    out.push({ text: m[0], match: true });
    from = i + m[0].length;
  }
  if (from < text.length || out.length === 0) out.push({ text: text.slice(from), match: false });
  return out;
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
