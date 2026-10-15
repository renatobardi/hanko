import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, requestJSON, type FetchLike } from "../shared/api";
import { makeT, type Msgs } from "../shared/island";
import { buildSegments, normalizeSelection, type AnchorView, type Selected } from "./segments";

export interface AnchorEditorProps {
  /** Texto do documento exatamente como o servidor o guarda (fins de linha normalizados em \n). */
  doc: string;
  anchors: AnchorView[];
  /** task_description | run_summary */
  target: "task_description" | "run_summary";
  /** Execução cujo resumo é o alvo (só com run_summary). */
  runId?: string;
  /** Chave da task (ex.: HNK-12). */
  taskKey: string;
  /** Ex.: /api/v1/companies/acme */
  apiBase: string;
  /** member+ comenta; viewer só lê. */
  canComment: boolean;
  msgs: Msgs;
  fetchImpl?: FetchLike;
  /** Chamado depois de um comentário gravado (o host atualiza a thread). */
  onPosted?: () => void;
}

interface PostedComment {
  id: string;
  body: string;
  anchor?: { quote: string; start: number; end: number };
  anchor_state?: { status: string; start: number; end: number };
}

interface Pending extends Selected {
  /** Posição do botão relativa ao contêiner. */
  left: number;
  top: number;
}

export function AnchorEditor(props: AnchorEditorProps) {
  const { doc, target, runId, taskKey, apiBase, canComment, msgs } = props;
  const doFetch: FetchLike = props.fetchImpl ?? ((u, i) => fetch(u, i));
  const t = useMemo(() => makeT(msgs), [msgs]);

  const [anchors, setAnchors] = useState<AnchorView[]>(props.anchors);
  const [pending, setPending] = useState<Pending | null>(null);
  const [composing, setComposing] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [active, setActive] = useState<string | null>(null);

  const wrap = useRef<HTMLDivElement>(null);
  const body = useRef<HTMLDivElement>(null);
  const composingRef = useRef(false);
  composingRef.current = composing;

  const segments = useMemo(() => buildSegments(doc, anchors), [doc, anchors]);
  const lost = anchors.filter((a) => a.status === "lost");
  const shown = active ? anchors.filter((a) => a.status !== "lost" && segmentCovers(segments, active, a.id)) : [];

  // seleção de texto no documento ⇒ botão "Comentar neste trecho"
  useEffect(() => {
    if (!canComment) return;
    const onSel = () => {
      if (composingRef.current) return; // com o formulário aberto a seleção congela
      const el = body.current;
      const sel = window.getSelection();
      if (!el || !sel || sel.rangeCount === 0 || sel.isCollapsed) return setPending(null);
      const range = sel.getRangeAt(0);
      if (!el.contains(range.startContainer) || !el.contains(range.endContainer)) return setPending(null);
      const a = utf16Offset(el, range.startContainer, range.startOffset);
      const b = utf16Offset(el, range.endContainer, range.endOffset);
      const s = normalizeSelection(doc, a, b);
      if (!s) return setPending(null);
      const r = range.getBoundingClientRect?.() ?? { left: 0, bottom: 0 }; // jsdom não mede
      const w = wrap.current?.getBoundingClientRect();
      setPending({ ...s, left: Math.max(0, r.left - (w?.left ?? 0)), top: r.bottom - (w?.top ?? 0) + 6 });
    };
    document.addEventListener("selectionchange", onSel);
    return () => document.removeEventListener("selectionchange", onSel);
  }, [canComment, doc]);

  const cancel = useCallback(() => {
    setComposing(false);
    setPending(null);
    setText("");
    setError(null);
    window.getSelection()?.removeAllRanges();
  }, []);

  const submit = async () => {
    if (!pending || busy) return;
    const trimmed = text.trim();
    if (!trimmed) return setError(t("anchor.err.empty"));
    setBusy(true);
    setError(null);
    try {
      const anchor: Record<string, unknown> = { target, quote: pending.quote, start: pending.start, end: pending.end };
      if (target === "run_summary" && runId) anchor.run_id = runId;
      const c = await requestJSON<PostedComment>(doFetch, `${apiBase}/tasks/${encodeURIComponent(taskKey)}/comments`, {
        method: "POST",
        body: JSON.stringify({ body: trimmed, anchor }),
      });
      const st = c.anchor_state ?? { status: "exact", start: pending.start, end: pending.end };
      setAnchors((prev) => [
        ...prev,
        { id: c.id, quote: c.anchor?.quote ?? pending.quote, start: st.start, end: st.end, status: st.status, author: t("anchor.you"), when: "", body: trimmed },
      ]);
      setNotice(t("anchor.sent"));
      cancel();
      props.onPosted?.();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("anchor.err.send"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="anchor-island" ref={wrap} data-testid="anchor-editor">
      <div className="anchor-doc md-plain" ref={body} aria-label={t("anchor.doc")}>
        {segments.map((s, i) =>
          s.ids.length === 0 ? (
            <span key={i}>{s.text}</span>
          ) : (
            <mark
              key={i}
              className={"anchor-mark" + (s.ids.includes(active ?? "") ? " is-active" : "")}
              data-ids={s.ids.join(" ")}
              tabIndex={0}
              role="button"
              aria-label={t("anchor.open")}
              onClick={() => setActive(s.ids.includes(active ?? "") ? null : s.ids[s.ids.length - 1]!)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  setActive(s.ids[s.ids.length - 1]!);
                }
              }}
            >
              {s.text}
            </mark>
          ),
        )}
      </div>

      {canComment && pending && !composing && (
        <button
          type="button"
          className="btn sm primary anchor-pop"
          style={{ left: pending.left, top: pending.top }}
          onMouseDown={(e) => e.preventDefault() /* não perder a seleção */}
          onClick={() => setComposing(true)}
        >
          {t("anchor.comment")}
        </button>
      )}

      {composing && pending && (
        <form
          className="anchor-form"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <blockquote className="anchor-quote">{pending.quote}</blockquote>
          <textarea
            className="textarea"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={t("anchor.placeholder")}
            aria-label={t("anchor.placeholder")}
            rows={3}
            // eslint-disable-next-line jsx-a11y/no-autofocus
            autoFocus
          />
          {error && (
            <div className="err-box" role="alert">
              {error}
            </div>
          )}
          <div className="anchor-actions">
            <button type="button" className="btn sm ghost" onClick={cancel}>
              {t("anchor.cancel")}
            </button>
            <button type="submit" className="btn sm primary" disabled={busy}>
              {busy ? t("anchor.sending") : t("anchor.send")}
            </button>
          </div>
        </form>
      )}

      {notice && !composing && (
        <p className="muted anchor-notice" role="status">
          {notice}
        </p>
      )}

      {shown.length > 0 && (
        <div className="anchor-thread" role="region" aria-label={t("anchor.thread")}>
          {shown.map((a) => (
            <AnchorCard key={a.id} a={a} t={t} />
          ))}
        </div>
      )}

      {lost.length > 0 && (
        <div className="anchor-lost">
          <div className="side-h">{t("anchor.lost.title")}</div>
          {lost.map((a) => (
            <AnchorCard key={a.id} a={a} t={t} lost />
          ))}
        </div>
      )}
    </div>
  );
}

function AnchorCard({ a, t, lost }: { a: AnchorView; t: ReturnType<typeof makeT>; lost?: boolean }) {
  return (
    <div className={"anchor-card" + (lost ? " is-lost" : "")}>
      <blockquote className="anchor-quote">{a.quote}</blockquote>
      <div className="meta">
        <b>{a.author}</b> {a.when && <time className="mono">{a.when}</time>}
        {lost && <span className="chip soft">{t("anchor.lost")}</span>}
        {!lost && a.status === "moved" && <span className="chip soft">{t("anchor.moved")}</span>}
      </div>
      <div className="anchor-body">{a.body}</div>
    </div>
  );
}

function segmentCovers(segments: { ids: string[] }[], activeId: string, id: string): boolean {
  // mostra os comentários de todas as âncoras que se sobrepõem à âncora ativa
  return id === activeId || segments.some((s) => s.ids.includes(activeId) && s.ids.includes(id));
}

/** Offset UTF-16 do ponto (node, offset) dentro do texto do contêiner. */
function utf16Offset(root: Node, node: Node, offset: number): number {
  const r = document.createRange();
  r.selectNodeContents(root);
  r.setEnd(node, offset);
  return r.toString().length;
}
