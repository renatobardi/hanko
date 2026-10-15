import { memo, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ReactElement } from "react";
import { ApiError, requestJSON, type FetchLike } from "../shared/api";
import { makeT, type Msgs } from "../shared/island";
import { clock, highlightParts, kindLabel, oneLine, prettyPayload } from "./format";
import { KINDS, filterEvents, lowerBoundSeq, mergeEvents, type RunEvent } from "./store";
import { makeLayout, visibleRange, ROW_H } from "./virtual";

export interface RunLogProps {
  /** Ex.: /api/v1/companies/acme */
  apiBase: string;
  runId: string;
  /** A execução ainda não terminou: segue o SSE depois da carga inicial. */
  live: boolean;
  tz: string;
  lang: string;
  msgs: Msgs;
  pageSize?: number;
  fetchImpl?: FetchLike;
  eventSourceImpl?: typeof EventSource;
}

const TERMINAL = new Set(["done", "interrupted", "failed", "cancelled", "expired"]);
const FLUSH_MS = 40;

type Conn = "live" | "retry" | "done";

export function RunLog(props: RunLogProps) {
  const { apiBase, runId, live, tz, lang, msgs, pageSize = 500 } = props;
  const doFetch: FetchLike = props.fetchImpl ?? ((u, i) => fetch(u, i));
  const ES = props.eventSourceImpl ?? (typeof EventSource !== "undefined" ? EventSource : undefined);
  const t = useMemo(() => makeT(msgs), [msgs]);

  const [events, setEvents] = useState<RunEvent[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [conn, setConn] = useState<Conn>(live ? "retry" : "done");
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set());
  const [query, setQuery] = useState("");
  const [follow, setFollow] = useState(true);
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(() => new Set());
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportH, setViewportH] = useState(480);
  const [reloadKey, setReloadKey] = useState(0);

  const vp = useRef<HTMLDivElement>(null);
  const restore = useRef<{ seq: number; delta: number } | null>(null);
  const loadingOlderRef = useRef(false);

  // ---- carga inicial (fim do log) + SSE para o resto -------------------------------------------------------
  useEffect(() => {
    let cancelled = false;
    let es: EventSource | null = null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const buf: RunEvent[] = [];
    const flush = () => {
      timer = undefined;
      if (cancelled || buf.length === 0) return;
      const batch = buf.splice(0);
      setEvents((prev) => mergeEvents(prev, batch));
    };
    const open = (after: number) => {
      if (!ES || cancelled) {
        setConn("done");
        return;
      }
      es = new ES(`${apiBase}/runs/${runId}/stream?after=${after}`);
      es.onopen = () => setConn("live");
      es.onerror = () => setConn((c) => (c === "done" ? c : "retry"));
      es.addEventListener("run.event", (m) => {
        try {
          buf.push(JSON.parse((m as MessageEvent).data as string) as RunEvent);
        } catch {
          return;
        }
        if (timer === undefined) timer = setTimeout(flush, FLUSH_MS);
      });
      es.addEventListener("run.status", (m) => {
        try {
          const st = (JSON.parse((m as MessageEvent).data as string) as { status?: string }).status ?? "";
          if (!TERMINAL.has(st)) return;
        } catch {
          return;
        }
        if (timer !== undefined) clearTimeout(timer);
        flush();
        es?.close();
        setConn("done");
      });
    };
    setError(null);
    (async () => {
      try {
        const page = await requestJSON<{ items: RunEvent[] }>(doFetch, `${apiBase}/runs/${runId}/events?tail=true&limit=${pageSize}`);
        if (cancelled) return;
        setEvents((prev) => mergeEvents(prev, page.items));
        setHasMore(page.items.length >= pageSize);
        setLoaded(true);
        if (live) open(page.items.length ? page.items[page.items.length - 1]!.seq : 0);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof ApiError ? e.message : t("log.error"));
        setLoaded(true);
      }
    })();
    return () => {
      cancelled = true;
      if (timer !== undefined) clearTimeout(timer);
      es?.close();
    };
    // doFetch/ES/t são estáveis na prática; a ilha remonta quando a execução muda.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiBase, runId, live, pageSize, reloadKey]);

  // ---- viewport -------------------------------------------------------------------------------------------
  useEffect(() => {
    const el = vp.current;
    if (!el) return;
    const measure = () => setViewportH(el.clientHeight || 480);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // ---- derivados ------------------------------------------------------------------------------------------
  const q = useDeferredValue(query);
  const visible = useMemo(() => filterEvents(events, { hidden, query: q }), [events, hidden, q]);
  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    for (const e of events) c[e.kind] = (c[e.kind] ?? 0) + 1;
    return c;
  }, [events]);
  const expandedIdx = useMemo(() => {
    const idx: number[] = [];
    for (const seq of expanded) {
      const i = lowerBoundSeq(visible, seq);
      if (visible[i]?.seq === seq) idx.push(i);
    }
    return idx.sort((a, b) => a - b);
  }, [expanded, visible]);
  const layout = useMemo(() => makeLayout(visible.length, expandedIdx), [visible.length, expandedIdx]);
  const [start, end] = visibleRange(layout, visible.length, scrollTop, viewportH);

  const latest = useRef({ layout, visible, scrollTop, hasMore, events });
  latest.current = { layout, visible, scrollTop, hasMore, events };

  const setTop = useCallback((top: number) => {
    const el = vp.current;
    if (el && el.scrollTop !== top) el.scrollTop = top;
    setScrollTop(top);
  }, []);

  // seguir o fim, ou manter a posição de leitura quando páginas antigas entram acima
  useLayoutEffect(() => {
    const r = restore.current;
    if (r) {
      restore.current = null;
      const i = lowerBoundSeq(visible, r.seq);
      setTop(layout.offsetOf(i) + r.delta);
      return;
    }
    if (follow) setTop(Math.max(0, layout.total - (vp.current?.clientHeight || viewportH)));
  }, [layout, visible, follow, viewportH, setTop]);

  const loadOlder = useCallback(async () => {
    const cur = latest.current;
    if (loadingOlderRef.current || !cur.hasMore || cur.events.length === 0) return;
    loadingOlderRef.current = true;
    setLoadingOlder(true);
    try {
      const first = cur.events[0]!.seq;
      const page = await requestJSON<{ items: RunEvent[] }>(doFetch, `${apiBase}/runs/${runId}/events?before=${first}&limit=${pageSize}`);
      const l = latest.current;
      const i = l.layout.indexAt(l.scrollTop);
      const a = l.visible[i];
      restore.current = a ? { seq: a.seq, delta: l.scrollTop - l.layout.offsetOf(i) } : null;
      setEvents((prev) => mergeEvents(prev, page.items));
      setHasMore(page.items.length >= pageSize);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("log.error"));
    } finally {
      loadingOlderRef.current = false;
      setLoadingOlder(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apiBase, runId, pageSize]);

  const onScroll = () => {
    const el = vp.current;
    if (!el) return;
    const st = el.scrollTop;
    setScrollTop(st);
    const h = el.clientHeight || viewportH;
    setFollow(layout.total - st - h < 40);
    if (st < ROW_H * 4 && latest.current.hasMore) void loadOlder();
  };

  const toggle = useCallback((seq: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (!next.delete(seq)) next.add(seq);
      return next;
    });
  }, []);
  const toggleKind = (k: string) =>
    setHidden((prev) => {
      const next = new Set(prev);
      if (!next.delete(k)) next.add(k);
      return next;
    });
  const goEnd = () => {
    setFollow(true);
    setTop(Math.max(0, layout.total - (vp.current?.clientHeight || viewportH)));
  };

  const filtered = hidden.size > 0 || q.trim() !== "";
  const kindsShown = KINDS.filter((k) => (counts[k] ?? 0) > 0 || hidden.has(k));

  const rows = [];
  for (let i = start; i < end; i++) {
    const e = visible[i]!;
    rows.push(
      <Row
        key={e.seq}
        e={e}
        index={i}
        total={visible.length}
        top={layout.offsetOf(i)}
        height={layout.heightOf(i)}
        open={expanded.has(e.seq)}
        onToggle={toggle}
        query={q}
        tz={tz}
        lang={lang}
        t={t}
      />,
    );
  }

  return (
    <div className="log-island" data-testid="run-log">
      <div className="log-bar">
        <input
          className="input log-search"
          type="search"
          value={query}
          onChange={(ev) => setQuery(ev.target.value)}
          placeholder={t("log.search")}
          aria-label={t("log.search")}
        />
        <div className="log-kinds" role="group" aria-label={t("log.filter")}>
          {kindsShown.map((k) => (
            <button key={k} type="button" className={"chip" + (hidden.has(k) ? " soft off" : "")} aria-pressed={!hidden.has(k)} onClick={() => toggleKind(k)}>
              {kindLabel(k)} <span className="n">{counts[k] ?? 0}</span>
            </button>
          ))}
        </div>
      </div>
      <div className="log log-viewport" ref={vp} role="log" aria-live="off" aria-label="Log" tabIndex={0} onScroll={onScroll}>
        <div className="log-spacer" role="list" style={{ height: layout.total }}>
          {rows}
        </div>
        {loaded && events.length === 0 && !error && <div className="log-empty log-note">{live ? t("log.waiting") : t("log.empty")}</div>}
        {events.length > 0 && visible.length === 0 && <div className="log-empty log-note">{t("log.nomatch")}</div>}
        {!loaded && <div className="log-empty log-note">{t("log.loading")}</div>}
      </div>
      {error && (
        <div className="err-box log-err" role="alert">
          {error}{" "}
          <button type="button" className="btn sm outline" onClick={() => setReloadKey((k) => k + 1)}>
            {t("log.retry")}
          </button>
        </div>
      )}
      <div className="log-status">
        <span>{filtered ? t("log.count.filtered", { shown: visible.length, total: events.length }) : t("log.count", { total: events.length })}</span>
        {hasMore && (
          <button type="button" className="btn sm ghost" onClick={() => void loadOlder()} disabled={loadingOlder}>
            {loadingOlder ? t("log.loading") : t("log.older")}
          </button>
        )}
        <span className="grow" />
        {live && conn !== "done" && <span className={"log-conn " + conn}>{conn === "live" ? t("log.live") : t("log.reconnecting")}</span>}
        {follow ? (
          live && conn !== "done" && <span className="muted">{t("log.following")}</span>
        ) : (
          <button type="button" className="btn sm outline" onClick={goEnd}>
            {t("log.paused")} · {t("log.follow")}
          </button>
        )}
      </div>
    </div>
  );
}

interface RowProps {
  e: RunEvent;
  index: number;
  total: number;
  top: number;
  height: number;
  open: boolean;
  onToggle: (seq: number) => void;
  query: string;
  tz: string;
  lang: string;
  t: ReturnType<typeof makeT>;
}

const Row = memo(function Row({ e, index, total, top, height, open, onToggle, query, tz, lang, t }: RowProps) {
  const hasPayload = e.payload !== undefined && e.payload !== null;
  const expandable = hasPayload || e.text.length > 160 || e.text.includes("\n");
  return (
    <div
      className={"log-line k-" + e.kind + (open ? " is-open" : "")}
      style={{ position: "absolute", left: 0, right: 0, top, height }}
      role="listitem"
      aria-posinset={index + 1}
      aria-setsize={total}
      data-seq={e.seq}
    >
      <time>{clock(e.occurred_at, tz, lang)}</time>
      <span className="k">{kindLabel(e.kind)}</span>
      {expandable ? (
        <button type="button" className="tx tx-btn" aria-expanded={open} onClick={() => onToggle(e.seq)} title={open ? t("log.collapse") : t("log.expand")}>
          <span className="car" aria-hidden="true">
            {open ? "▾" : "▸"}
          </span>
          {open ? <span className="tx-head">{oneLine(e.text, 200)}</span> : <Highlight text={oneLine(e.text)} q={query} />}
        </button>
      ) : (
        <span className="tx tx-line">
          <Highlight text={oneLine(e.text)} q={query} />
        </span>
      )}
      {open && (
        <div className="log-detail">
          {e.text && <pre className="log-full">{e.text}</pre>}
          {hasPayload && (
            <>
              <div className="log-detail-h">{t("log.payload")}</div>
              <pre className="log-payload">{prettyPayload(e.payload)}</pre>
            </>
          )}
        </div>
      )}
    </div>
  );
});

function Highlight({ text, q }: { text: string; q: string }) {
  const parts = highlightParts(text, q);
  if (parts.every((p) => !p.match)) return <>{text}</>;
  const out: (string | ReactElement)[] = parts.map((p, i) => (p.match ? <mark key={i}>{p.text}</mark> : p.text));
  return <>{out}</>;
}
