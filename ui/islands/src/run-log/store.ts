/** Evento do log de uma execução (mesmo formato do GET /runs/{id}/events e do SSE `run.event`). */
export interface RunEvent {
  id: string;
  run_id: string;
  seq: number;
  kind: string;
  text: string;
  payload?: unknown;
  occurred_at: string;
  received_at?: string;
}

export const KINDS = ["system", "assistant", "thinking", "tool_use", "tool_result", "stdout", "stderr", "result", "status"] as const;

/**
 * Junta `incoming` na lista ordenada por seq sem perder nem repetir: (run_id, seq) é único no servidor, então
 * seq é a chave de dedupe. Devolve a mesma lista (mesma referência) se não houve nada novo.
 */
export function mergeEvents(list: RunEvent[], incoming: RunEvent[]): RunEvent[] {
  if (incoming.length === 0) return list;
  const lastSeq = list.length ? list[list.length - 1]!.seq : -Infinity;
  const sorted = isAscending(incoming) ? incoming : [...incoming].sort((a, b) => a.seq - b.seq);

  // caminho comum (ao vivo e página para frente): tudo vem depois do último
  if (sorted[0]!.seq > lastSeq) return dedupeSorted(list, sorted);
  // página mais antiga: tudo vem antes do primeiro
  if (list.length && sorted[sorted.length - 1]!.seq < list[0]!.seq) return dedupeSorted(sorted, list);

  // sobreposição qualquer (reconexão, replay, páginas cruzadas)
  const bySeq = new Map<number, RunEvent>();
  for (const e of list) bySeq.set(e.seq, e);
  let added = false;
  for (const e of sorted) {
    if (!bySeq.has(e.seq)) {
      bySeq.set(e.seq, e);
      added = true;
    }
  }
  if (!added) return list;
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq);
}

function isAscending(es: RunEvent[]): boolean {
  for (let i = 1; i < es.length; i++) if (es[i]!.seq < es[i - 1]!.seq) return false;
  return true;
}

/** Concatena a + b (ambos ordenados, todos de a < todos de b) removendo seq repetido dentro de cada lado. */
function dedupeSorted(a: RunEvent[], b: RunEvent[]): RunEvent[] {
  const out = a.slice();
  let prev = out.length ? out[out.length - 1]!.seq : -Infinity;
  for (const e of b) {
    if (e.seq === prev) continue;
    out.push(e);
    prev = e.seq;
  }
  return out;
}

export interface Filter {
  hidden: ReadonlySet<string>;
  query: string;
}

export function matchesFilter(e: RunEvent, f: Filter): boolean {
  if (f.hidden.has(e.kind)) return false;
  const q = f.query.trim().toLowerCase();
  if (!q) return true;
  return e.text.toLowerCase().includes(q) || e.kind.includes(q);
}

/** Subconjunto visível (preserva a ordem). Sem filtro devolve a própria lista. */
export function filterEvents(list: RunEvent[], f: Filter): RunEvent[] {
  if (f.hidden.size === 0 && !f.query.trim()) return list;
  return list.filter((e) => matchesFilter(e, f));
}

/** Índice do primeiro evento com seq >= alvo (lista ordenada); list.length se não houver. */
export function lowerBoundSeq(list: RunEvent[], seq: number): number {
  let lo = 0;
  let hi = list.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (list[mid]!.seq < seq) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}
