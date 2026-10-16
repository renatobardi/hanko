/** Persona no organograma (formato entregue pelo host em data-nodes). */
export interface OrgNode {
  id: string;
  slug: string;
  name: string;
  title: string;
  /** id da persona superior; "" = topo (reporta ao dono). */
  parent: string;
  /** Rótulo de status já traduzido pelo host. */
  status: string;
  /** idle | run | error | paused | gate */
  cls: string;
  meta: string;
  href: string;
  gate: boolean;
  initials: string;
  /** Glifo da persona: o host define um <symbol id="pg-{icon}"> por glifo na página. */
  icon?: string;
}

// Mesma geometria do host (internal/ui/handlers_org.go): 1 unidade = 1 px.
export const CARD_W = 232;
export const CARD_H = 84;
export const GAP_X = 28;
export const GAP_Y = 52;
export const PAD = 24;

export interface Placed {
  id: string;
  x: number;
  y: number;
  depth: number;
  /** Quantidade de descendentes escondidos por este nó estar colapsado. */
  hiddenBelow: number;
  hasChildren: boolean;
}

export interface Edge {
  from: string;
  to: string;
  d: string;
}

export interface Layout {
  placed: Placed[];
  byId: Map<string, Placed>;
  edges: Edge[];
  width: number;
  height: number;
}

export function childrenMap(nodes: readonly OrgNode[]): Map<string, string[]> {
  const ids = new Set(nodes.map((n) => n.id));
  const m = new Map<string, string[]>();
  for (const n of nodes) {
    const p = n.parent && ids.has(n.parent) ? n.parent : "";
    const list = m.get(p);
    if (list) list.push(n.id);
    else m.set(p, [n.id]);
  }
  return m;
}

/** Folhas ocupam colunas consecutivas; o pai fica centrado sobre os filhos; cada profundidade é uma linha. */
export function layoutTree(nodes: readonly OrgNode[], collapsed: ReadonlySet<string>): Layout {
  const kids = childrenMap(nodes);
  const placed: Placed[] = [];
  const byId = new Map<string, Placed>();
  const edges: Edge[] = [];
  let col = 0;
  let maxDepth = -1;

  const countBelow = (id: string): number => {
    let n = 0;
    for (const c of kids.get(id) ?? []) n += 1 + countBelow(c);
    return n;
  };

  const place = (id: string, depth: number): Placed => {
    const all = kids.get(id) ?? [];
    const isCollapsed = collapsed.has(id) && all.length > 0;
    const shown = isCollapsed ? [] : all.map((c) => place(c, depth + 1));
    let cx: number;
    if (shown.length === 0) {
      cx = PAD + col * (CARD_W + GAP_X) + CARD_W / 2;
      col++;
    } else {
      cx = (shown[0]!.x + shown[shown.length - 1]!.x + CARD_W) / 2;
    }
    const p: Placed = {
      id,
      x: cx - CARD_W / 2,
      y: PAD + depth * (CARD_H + GAP_Y),
      depth,
      hiddenBelow: isCollapsed ? countBelow(id) : 0,
      hasChildren: all.length > 0,
    };
    maxDepth = Math.max(maxDepth, depth);
    placed.push(p);
    byId.set(id, p);
    for (const c of shown) {
      const px = p.x + CARD_W / 2;
      const py = p.y + CARD_H;
      const cx2 = c.x + CARD_W / 2;
      const mid = py + GAP_Y / 2;
      edges.push({ from: id, to: c.id, d: `M${px} ${py} V${mid} H${cx2} V${c.y}` });
    }
    return p;
  };

  for (const r of kids.get("") ?? []) place(r, 0);
  const cols = Math.max(col, 1);
  return {
    placed,
    byId,
    edges,
    width: PAD * 2 + cols * CARD_W + (cols - 1) * GAP_X,
    height: PAD * 2 + (maxDepth + 1) * CARD_H + Math.max(maxDepth, 0) * GAP_Y,
  };
}

/** `id` está abaixo de `ancestor` (direta ou indiretamente)? Protegido contra laços. */
export function isBelow(nodes: readonly OrgNode[], ancestor: string, id: string): boolean {
  const parent = new Map(nodes.map((n) => [n.id, n.parent]));
  let cur = parent.get(id);
  for (let i = 0; cur && i <= nodes.length; i++) {
    if (cur === ancestor) return true;
    cur = parent.get(cur);
  }
  return false;
}

export type MoveCheck = { ok: true } | { ok: false; reason: "self" | "cycle" | "same" };

/** Mover `id` para baixo de `newParent` ("" = topo) é válido? O service valida de novo (422 em ciclo). */
export function checkMove(nodes: readonly OrgNode[], id: string, newParent: string): MoveCheck {
  const cur = nodes.find((n) => n.id === id);
  if (!cur) return { ok: false, reason: "self" };
  if (newParent === id) return { ok: false, reason: "self" };
  if (cur.parent === newParent) return { ok: false, reason: "same" };
  if (newParent && isBelow(nodes, id, newParent)) return { ok: false, reason: "cycle" };
  return { ok: true };
}

/** Clamp do zoom. */
export const MIN_ZOOM = 0.25;
export const MAX_ZOOM = 2;
export const clampZoom = (k: number): number => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));
