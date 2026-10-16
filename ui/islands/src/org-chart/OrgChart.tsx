import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { ApiError, requestJSON, type FetchLike } from "../shared/api";
import { makeT, type Msgs } from "../shared/island";
import { CARD_H, CARD_W, MAX_ZOOM, MIN_ZOOM, checkMove, clampZoom, layoutTree, type OrgNode } from "./layout";

export interface OrgChartProps {
  nodes: OrgNode[];
  /** Admin/owner: só eles arrastam (o service confere de novo). */
  canEdit: boolean;
  /** Ex.: /api/v1/companies/acme */
  apiBase: string;
  msgs: Msgs;
  fetchImpl?: FetchLike;
}

interface View {
  k: number;
  x: number;
  y: number;
}

interface DragState {
  id: string;
  startX: number;
  startY: number;
  active: boolean;
  /** Posição do ponteiro em coordenadas do grafo. */
  gx: number;
  gy: number;
  /** Alvo sob o ponteiro: id, "" = zona "topo", null = nenhum. */
  over: string | null;
}

const DRAG_THRESHOLD = 5;

export function OrgChart(props: OrgChartProps) {
  const { canEdit, apiBase, msgs } = props;
  const doFetch: FetchLike = props.fetchImpl ?? ((u, i) => fetch(u, i));
  const t = useMemo(() => makeT(msgs), [msgs]);

  const [nodes, setNodes] = useState<OrgNode[]>(props.nodes);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set());
  const [view, setView] = useState<View>({ k: 1, x: 8, y: 8 });
  const [drag, setDrag] = useState<DragState | null>(null);
  const [notice, setNotice] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const stage = useRef<HTMLDivElement>(null);
  const rootZone = useRef<HTMLDivElement>(null);
  const dragRef = useRef<DragState | null>(null);
  const panRef = useRef<{ sx: number; sy: number; vx: number; vy: number } | null>(null);
  const suppressClick = useRef(false);
  const viewRef = useRef(view);
  viewRef.current = view;

  const layout = useMemo(() => layoutTree(nodes, collapsed), [nodes, collapsed]);
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);

  // enquadra o organograma na largura disponível ao montar
  useLayoutEffect(() => {
    const w = stage.current?.clientWidth ?? 0;
    if (w <= 0) return;
    const k = clampZoom(Math.min(1, (w - 16) / layout.width));
    setView({ k, x: Math.max(8, (w - layout.width * k) / 2), y: 8 });
    // só na montagem: depois disso o usuário comanda o zoom
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // zoom por Ctrl/Cmd + roda (inclui o pinch do trackpad); a roda comum continua rolando a página
  useEffect(() => {
    const el = stage.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      const r = el.getBoundingClientRect();
      zoomAt(e.clientX - r.left, e.clientY - r.top, Math.exp(-e.deltaY * 0.01));
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const zoomAt = (px: number, py: number, factor: number) => {
    setView((v) => {
      const k = clampZoom(v.k * factor);
      const f = k / v.k;
      return { k, x: px - (px - v.x) * f, y: py - (py - v.y) * f };
    });
  };
  const zoomCenter = (factor: number) => {
    const r = stage.current?.getBoundingClientRect();
    zoomAt((r?.width ?? 0) / 2, (r?.height ?? 0) / 2, factor);
  };
  const fit = () => {
    const w = stage.current?.clientWidth ?? 0;
    if (w <= 0) return setView({ k: 1, x: 8, y: 8 });
    const k = clampZoom(Math.min(1, (w - 16) / layout.width));
    setView({ k, x: Math.max(8, (w - layout.width * k) / 2), y: 8 });
  };

  const toGraph = (clientX: number, clientY: number) => {
    const r = stage.current?.getBoundingClientRect();
    const v = viewRef.current;
    return { gx: (clientX - (r?.left ?? 0) - v.x) / v.k, gy: (clientY - (r?.top ?? 0) - v.y) / v.k };
  };

  const hit = (clientX: number, clientY: number, gx: number, gy: number, self: string): string | null => {
    const z = rootZone.current?.getBoundingClientRect();
    if (z && z.width > 0 && clientX >= z.left && clientX <= z.right && clientY >= z.top && clientY <= z.bottom) return "";
    for (const p of layout.placed) {
      if (p.id !== self && gx >= p.x && gx <= p.x + CARD_W && gy >= p.y && gy <= p.y + CARD_H) return p.id;
    }
    return null;
  };

  const nameOf = (id: string) => byId.get(id)?.name ?? "";

  const move = useCallback(
    async (id: string, newParent: string) => {
      const node = byId.get(id);
      if (!node) return;
      const check = checkMove(nodes, id, newParent);
      if (!check.ok) {
        if (check.reason === "cycle") setNotice({ kind: "err", text: t("org.err.cycle", { name: node.name, to: nameOf(newParent) }) });
        else if (check.reason === "self") setNotice({ kind: "err", text: t("org.err.self") });
        return;
      }
      const prev = nodes;
      setNodes(nodes.map((n) => (n.id === id ? { ...n, parent: newParent } : n)));
      setBusy(true);
      try {
        const body = newParent ? { reports_to: byId.get(newParent)?.slug } : { clear_reports_to: true };
        await requestJSON(doFetch, `${apiBase}/personas/${encodeURIComponent(node.slug)}`, { method: "PATCH", body: JSON.stringify(body) });
        setNotice({ kind: "ok", text: newParent ? t("org.moved", { name: node.name, to: nameOf(newParent) }) : t("org.moved.top", { name: node.name }) });
      } catch (e) {
        setNodes(prev);
        setNotice({ kind: "err", text: t("org.err.api", { err: e instanceof ApiError ? e.message : String(e) }) });
      } finally {
        setBusy(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [nodes, byId, apiBase, t],
  );

  // ---- ponteiro: arrastar persona (editores) e panorâmica no fundo ---------------------------------------
  const onPointerDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    const target = e.target as Element;
    if (target.closest("[data-toggle]")) return;
    const nodeEl = target.closest("[data-node]");
    if (nodeEl) {
      if (!canEdit || busy) return;
      const id = nodeEl.getAttribute("data-node") ?? "";
      const { gx, gy } = toGraph(e.clientX, e.clientY);
      const d: DragState = { id, startX: e.clientX, startY: e.clientY, active: false, gx, gy, over: null };
      dragRef.current = d;
      return;
    }
    panRef.current = { sx: e.clientX, sy: e.clientY, vx: viewRef.current.x, vy: viewRef.current.y };
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
  };

  const onPointerMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const d = dragRef.current;
    if (d) {
      if (!d.active && Math.hypot(e.clientX - d.startX, e.clientY - d.startY) < DRAG_THRESHOLD) return;
      const { gx, gy } = toGraph(e.clientX, e.clientY);
      const next: DragState = { ...d, active: true, gx, gy, over: hit(e.clientX, e.clientY, gx, gy, d.id) };
      dragRef.current = next;
      if (!d.active) (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
      setDrag(next);
      return;
    }
    const p = panRef.current;
    if (p) setView((v) => ({ ...v, x: p.vx + (e.clientX - p.sx), y: p.vy + (e.clientY - p.sy) }));
  };

  const onPointerUp = () => {
    const d = dragRef.current;
    dragRef.current = null;
    panRef.current = null;
    if (!d) return;
    setDrag(null);
    if (!d.active) return;
    suppressClick.current = true; // o clique que o navegador gera depois do arrasto não pode abrir a persona
    setTimeout(() => (suppressClick.current = false), 0);
    if (d.over !== null) void move(d.id, d.over);
  };

  const toggleCollapse = (id: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  const dragging = drag?.active ? drag : null;
  const overCheck = dragging && dragging.over !== null ? checkMove(nodes, dragging.id, dragging.over) : null;
  const anyCollapsed = collapsed.size > 0;

  return (
    <div className="org-island" data-testid="org-chart">
      <div className="org-toolbar" role="toolbar" aria-label={t("org.toolbar")}>
        <button type="button" className="btn sm outline" onClick={() => zoomCenter(1.2)} disabled={view.k >= MAX_ZOOM} aria-label={t("org.zoom.in")}>
          +
        </button>
        <button type="button" className="btn sm outline" onClick={() => zoomCenter(1 / 1.2)} disabled={view.k <= MIN_ZOOM} aria-label={t("org.zoom.out")}>
          −
        </button>
        <button type="button" className="btn sm outline" onClick={fit}>
          {t("org.fit")}
        </button>
        {anyCollapsed && (
          <button type="button" className="btn sm outline" onClick={() => setCollapsed(new Set())}>
            {t("org.expand.all")}
          </button>
        )}
        <span className="org-zoom muted">{Math.round(view.k * 100)}%</span>
        <span className="grow" />
        <span className="muted org-hint">{canEdit ? t("org.hint.edit") : t("org.hint.view")}</span>
      </div>
      <div
        className={"org-stage" + (dragging ? " is-dragging" : "")}
        ref={stage}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
      >
        {dragging && (
          <div ref={rootZone} className={"org-root-drop" + (dragging.over === "" && overCheck?.ok ? " is-over" : "")}>
            {t("org.drop.top")}
          </div>
        )}
        <svg className="org-canvas" width="100%" height="100%" role="group" aria-label={t("org.label")}>
          <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
            {layout.edges.map((e) => (
              <path key={e.from + e.to} className="org-edge" d={e.d} />
            ))}
            {layout.placed.map((p) => {
              const n = byId.get(p.id);
              if (!n) return null;
              const over = dragging?.over === p.id ? (overCheck?.ok ? " drop-ok" : " drop-bad") : "";
              return (
                <g
                  key={p.id}
                  data-node={p.id}
                  transform={`translate(${p.x} ${p.y})`}
                  className={`org-node st-${n.cls}${n.gate ? " gate" : ""}${canEdit ? " movable" : ""}${dragging?.id === p.id ? " is-dragged" : ""}${over}`}
                >
                  <a
                    href={n.href}
                    aria-label={`${n.name}, ${n.title}, ${n.status}`}
                    draggable={false}
                    onClick={(ev) => {
                      if (suppressClick.current) ev.preventDefault();
                    }}
                  >
                    <rect width={CARD_W} height={CARD_H} rx={12} />
                    <circle className="org-av" cx={26} cy={28} r={14} />
                    <use className="org-glyph" href={`#pg-${n.icon || "bot"}`} x={18} y={20} width={16} height={16} />
                    <text className="org-name" x={50} y={25}>
                      {n.name}
                    </text>
                    <text className="org-title" x={50} y={42}>
                      {n.title}
                    </text>
                    {n.cls === "run" && <circle className="org-pulse" cx={16} cy={64} r={3.5} />}
                    <circle className="org-dot" cx={16} cy={64} r={3.5} />
                    <text className="org-meta" x={26} y={68}>
                      {n.status}
                      {n.meta}
                    </text>
                  </a>
                  {p.hasChildren && (
                    <g
                      data-toggle=""
                      className="org-toggle"
                      role="button"
                      tabIndex={0}
                      aria-expanded={p.hiddenBelow === 0}
                      aria-label={p.hiddenBelow === 0 ? t("org.collapse", { name: n.name }) : t("org.expand", { name: n.name, n: p.hiddenBelow })}
                      transform={`translate(${CARD_W / 2} ${CARD_H})`}
                      onClick={(ev) => {
                        ev.stopPropagation();
                        toggleCollapse(p.id);
                      }}
                      onKeyDown={(ev) => {
                        if (ev.key === "Enter" || ev.key === " ") {
                          ev.preventDefault();
                          toggleCollapse(p.id);
                        }
                      }}
                    >
                      <circle r={10} />
                      <text textAnchor="middle" y={4}>
                        {p.hiddenBelow === 0 ? "−" : `+${p.hiddenBelow}`}
                      </text>
                    </g>
                  )}
                </g>
              );
            })}
            {dragging && (
              <g className="org-ghost" transform={`translate(${dragging.gx - CARD_W / 2} ${dragging.gy - CARD_H / 2})`} pointerEvents="none">
                <rect width={CARD_W} height={CARD_H} rx={12} />
                <text x={16} y={30} className="org-name">
                  {nameOf(dragging.id)}
                </text>
              </g>
            )}
          </g>
        </svg>
      </div>
      <div className="org-live" role={notice?.kind === "err" ? "alert" : "status"} aria-live="polite">
        {notice && <span className={"org-notice " + notice.kind}>{notice.text}</span>}
      </div>
    </div>
  );
}
