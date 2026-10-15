/** Âncora já resolvida pelo host contra a versão atual do documento (offsets em code points). */
export interface AnchorView {
  id: string;
  quote: string;
  start: number;
  end: number;
  /** exact | moved | lost (lost = âncora perdida: o trecho não existe mais). */
  status: string;
  author: string;
  when: string;
  body: string;
}

export interface Segment {
  text: string;
  /** Âncoras que cobrem o trecho (vazio = texto comum). Sobreposição gera mais de um id. */
  ids: string[];
}

/** Divide o documento em trechos comuns e trechos cobertos por âncoras (âncoras perdidas não entram). */
export function buildSegments(doc: string, anchors: readonly AnchorView[]): Segment[] {
  const cps = Array.from(doc);
  const placed = anchors.filter((a) => a.status !== "lost" && a.end > a.start && a.start >= 0 && a.start < cps.length);
  const cuts = new Set<number>([0, cps.length]);
  for (const a of placed) {
    cuts.add(a.start);
    cuts.add(Math.min(a.end, cps.length));
  }
  const pts = [...cuts].sort((x, y) => x - y);
  const out: Segment[] = [];
  for (let i = 0; i + 1 < pts.length; i++) {
    const from = pts[i]!;
    const to = pts[i + 1]!;
    if (to <= from) continue;
    const ids = placed.filter((a) => a.start <= from && Math.min(a.end, cps.length) >= to).map((a) => a.id);
    out.push({ text: cps.slice(from, to).join(""), ids });
  }
  return out;
}

/** Offset em UTF-16 (DOM) → offset em code points (servidor). */
export function toCodePoints(s: string, utf16: number): number {
  return Array.from(s.slice(0, utf16)).length;
}

export interface Selected {
  quote: string;
  /** code points, [start, end) */
  start: number;
  end: number;
}

/** Normaliza uma seleção em UTF-16 do documento: sem espaços nas pontas; vazio/longo demais ⇒ null. */
export function normalizeSelection(doc: string, a16: number, b16: number, maxRunes = 2000): Selected | null {
  let s = Math.max(0, Math.min(a16, b16));
  let e = Math.min(doc.length, Math.max(a16, b16));
  while (s < e && /\s/.test(doc[s]!)) s++;
  while (e > s && /\s/.test(doc[e - 1]!)) e--;
  if (e <= s) return null;
  const quote = doc.slice(s, e);
  const n = Array.from(quote).length;
  if (n > maxRunes) return null;
  const start = toCodePoints(doc, s);
  return { quote, start, end: start + n };
}
