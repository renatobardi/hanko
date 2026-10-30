import { childrenMap, type OrgNode } from "./layout";

/**
 * Recorte do organograma por tag e por nível (ADR-0087 itens 6 a 8). Roda sobre a árvore que o host já entregou: não há
 * consulta nova por filtro. Nível 1 é o topo; o nível de cada persona é o do superior mais um.
 */

/** Tag do cadastro, como o host entrega em data-tags. */
export interface TagInfo {
  id: string;
  name: string;
  /** Nome da paleta (gray, red, amber, green, teal, blue, violet, pink). */
  color: string;
  description: string;
  archived: boolean;
}

export type Mode = "highlight" | "prune";

export interface OrgFilter {
  /** ids das tags escolhidas */
  tags: string[];
  /** true = a persona precisa ter todas (E); false = alguma (OU) */
  all: boolean;
  /** inclui as personas sem tag (por OU com as tags) */
  untagged: boolean;
  /** mostra as tags arquivadas na lista do filtro */
  archived: boolean;
  /** níveis marcados; vazio = todos */
  levels: number[];
  /** corta abaixo deste nível antes dos outros critérios; 0 = sem corte */
  maxDepth: number;
  mode: Mode;
}

export const emptyFilter: OrgFilter = { tags: [], all: false, untagged: false, archived: false, levels: [], maxDepth: 0, mode: "highlight" };

/** Algum critério (tag, sem tag ou nível) está ligado? A profundidade sozinha só corta, não recorta. */
export const isActive = (f: OrgFilter): boolean => f.tags.length > 0 || f.untagged || f.levels.length > 0;

/** Nível de cada persona: 1 no topo (sem superior ou com superior que não está na árvore). */
export function levelsOf(nodes: readonly OrgNode[]): Map<string, number> {
  const kids = childrenMap(nodes);
  const out = new Map<string, number>();
  const walk = (id: string, level: number) => {
    out.set(id, level);
    for (const c of kids.get(id) ?? []) walk(c, level + 1);
  };
  for (const r of kids.get("") ?? []) walk(r, 1);
  return out;
}

/** Normaliza o filtro do host ou da URL: tira tag que não existe e tag arquivada sem "mostrar arquivadas". */
export function sanitize(f: Partial<OrgFilter> | null | undefined, tags: readonly TagInfo[]): OrgFilter {
  const byId = new Map(tags.map((t) => [t.id, t]));
  const archived = !!f?.archived;
  const ids = (f?.tags ?? []).filter((id) => {
    const t = byId.get(id);
    return !!t && (archived || !t.archived);
  });
  const levels = [...new Set((f?.levels ?? []).filter((n) => Number.isInteger(n) && n >= 1))].sort((a, b) => a - b);
  const maxDepth = Number.isInteger(f?.maxDepth) && (f?.maxDepth ?? 0) > 0 ? (f?.maxDepth as number) : 0;
  return { tags: [...new Set(ids)], all: !!f?.all, untagged: !!f?.untagged, archived, levels, maxDepth, mode: f?.mode === "prune" ? "prune" : "highlight" };
}

function tagMatch(f: OrgFilter, tags: readonly string[]): boolean {
  if (f.tags.length === 0 && !f.untagged) return true;
  if (f.untagged && tags.length === 0) return true;
  if (f.tags.length === 0) return false;
  return f.all ? f.tags.every((t) => tags.includes(t)) : f.tags.some((t) => tags.includes(t));
}

export interface Slice {
  /** personas desenhadas */
  visible: Set<string>;
  /** personas desenhadas esmaecidas (fora do recorte, ou só caminho no modo Podar) */
  dim: Set<string>;
  /** no modo Podar: superiores que só aparecem como caminho */
  path: Set<string>;
  /** quantas personas entram no recorte */
  matched: number;
  active: boolean;
}

/**
 * Aplica o filtro. A profundidade máxima corta primeiro; tag e níveis se combinam por E. Destacar mantém a árvore
 * (dentro da profundidade) e esmaece quem está fora; Podar mantém só o recorte e os superiores até o topo, esmaecidos.
 */
export function applyFilter(nodes: readonly OrgNode[], f: OrgFilter): Slice {
  const level = levelsOf(nodes);
  const within = nodes.filter((n) => f.maxDepth === 0 || (level.get(n.id) ?? 1) <= f.maxDepth);
  const active = isActive(f);
  const matched = new Set(
    within.filter((n) => (f.levels.length === 0 || f.levels.includes(level.get(n.id) ?? 1)) && tagMatch(f, n.tags ?? [])).map((n) => n.id),
  );
  const visible = new Set(within.map((n) => n.id));
  if (!active) return { visible, dim: new Set(), path: new Set(), matched: matched.size, active };
  if (f.mode === "highlight") {
    const dim = new Set([...visible].filter((id) => !matched.has(id)));
    return { visible, dim, path: new Set(), matched: matched.size, active };
  }
  const parent = new Map(nodes.map((n) => [n.id, n.parent]));
  const shown = new Set(matched);
  const path = new Set<string>();
  for (const id of matched) {
    let cur = parent.get(id);
    for (let i = 0; cur && parent.has(cur) && i <= nodes.length; i++) {
      if (!shown.has(cur)) {
        shown.add(cur);
        path.add(cur);
      }
      cur = parent.get(cur);
    }
  }
  return { visible: shown, dim: new Set(path), path, matched: matched.size, active };
}

/** Parâmetros da URL e do cookie hanko_org (os mesmos que o host lê em internal/ui/org_filter.go). */
export function filterToQuery(f: OrgFilter): URLSearchParams {
  const q = new URLSearchParams();
  if (f.tags.length) q.set("tag", f.tags.join(","));
  if (f.all) q.set("todas", "1");
  if (f.untagged) q.set("semtag", "1");
  if (f.archived) q.set("arquivadas", "1");
  if (f.levels.length) q.set("nivel", f.levels.join(","));
  if (f.maxDepth > 0) q.set("prof", String(f.maxDepth));
  if (f.mode === "prune") q.set("modo", "podar");
  return q;
}

const FILTER_KEYS = ["tag", "todas", "semtag", "arquivadas", "nivel", "prof", "modo"];

/** Guarda o filtro na URL (sem recarregar) e no cookie hanko_org, que o host lê quando a URL não traz filtro. */
export function persistFilter(f: OrgFilter, loc: Location = window.location, doc: Document = document): void {
  const q = filterToQuery(f);
  const url = new URL(loc.href);
  for (const k of FILTER_KEYS) url.searchParams.delete(k);
  for (const [k, v] of q) url.searchParams.set(k, v);
  try {
    window.history.replaceState(window.history.state, "", url.pathname + url.search + url.hash);
  } catch {
    // sem history (ambiente de teste restrito): a URL fica como está
  }
  const secure = loc.protocol === "https:" ? "; Secure" : "";
  doc.cookie = `hanko_org=${encodeURIComponent(q.toString())}; Path=/; Max-Age=31536000; SameSite=Lax${secure}`;
}
