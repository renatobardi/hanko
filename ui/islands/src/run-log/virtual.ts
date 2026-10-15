/** Janela virtualizada de linhas de altura fixa; linhas expandidas ganham uma altura extra fixa. */
export const ROW_H = 24;
export const EXPANDED_EXTRA = 176;

export interface Layout {
  total: number;
  offsetOf(i: number): number;
  heightOf(i: number): number;
  /** Maior índice cujo início é <= y (0 se vazio). */
  indexAt(y: number): number;
}

/** `expanded`: índices (ordenados, sem repetição) das linhas expandidas. */
export function makeLayout(count: number, expanded: readonly number[], rowH = ROW_H, extra = EXPANDED_EXTRA): Layout {
  const before = (i: number): number => {
    let lo = 0;
    let hi = expanded.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (expanded[mid]! < i) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  };
  const isExpanded = (i: number): boolean => {
    const k = before(i);
    return k < expanded.length && expanded[k] === i;
  };
  const offsetOf = (i: number): number => i * rowH + extra * before(i);
  return {
    total: count * rowH + extra * expanded.length,
    offsetOf,
    heightOf: (i) => rowH + (isExpanded(i) ? extra : 0),
    indexAt(y) {
      if (count === 0) return 0;
      let lo = 0;
      let hi = count - 1;
      while (lo < hi) {
        const mid = (lo + hi + 1) >>> 1;
        if (offsetOf(mid) <= y) lo = mid;
        else hi = mid - 1;
      }
      return lo;
    },
  };
}

/** Faixa [start, end) a renderizar para a rolagem atual, com folga (overscan) nas duas pontas. */
export function visibleRange(layout: Layout, count: number, scrollTop: number, viewportH: number, overscan = 8): [number, number] {
  if (count === 0) return [0, 0];
  const first = layout.indexAt(Math.max(0, scrollTop));
  const last = layout.indexAt(Math.max(0, scrollTop) + Math.max(0, viewportH));
  return [Math.max(0, first - overscan), Math.min(count, last + 1 + overscan)];
}
