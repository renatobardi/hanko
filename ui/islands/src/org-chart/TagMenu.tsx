import { useState } from "react";
import type { T } from "../shared/island";
import type { TagInfo } from "./filter";

export interface TagMenuProps {
  personaName: string;
  catalog: readonly TagInfo[];
  /** ids das tags que a persona tem */
  applied: readonly string[];
  busy: boolean;
  left: number;
  top: number;
  onToggle: (tagId: string, on: boolean) => void;
  onCreate: (name: string) => void;
  onClose: () => void;
  t: T;
}

/**
 * Menu de tags do nó (ADR-0087 item 5): marcar aplica, desmarcar tira. Tag arquivada só aparece se a persona já tem, e
 * só sai. Digitar um nome que não existe oferece "criar tag «nome»", que cria e aplica numa transação só.
 */
export function TagMenu({ personaName, catalog, applied, busy, left, top, onToggle, onCreate, onClose, t }: TagMenuProps) {
  const [q, setQ] = useState("");
  const name = q.trim();
  const key = name.toLowerCase();
  const list = catalog.filter((tg) => (!tg.archived || applied.includes(tg.id)) && (!key || tg.name.toLowerCase().includes(key)));
  const exists = catalog.some((tg) => tg.name.toLowerCase() === key);
  return (
    <div
      className="org-tagmenu"
      role="dialog"
      aria-label={t("org.tags.menu", { name: personaName })}
      style={{ left, top }}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <input
        className="input sm"
        autoFocus
        value={q}
        maxLength={40}
        placeholder={t("org.tags.search")}
        aria-label={t("org.tags.search")}
        onChange={(e) => setQ(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && name && !exists && !busy) onCreate(name);
        }}
      />
      {list.map((tg) => {
        const on = applied.includes(tg.id);
        return (
          <label key={tg.id}>
            <input type="checkbox" checked={on} disabled={busy || (tg.archived && !on)} onChange={() => onToggle(tg.id, !on)} />
            <span className={`tag-chip tag-c-${tg.color}${tg.archived ? " archived" : ""}`} title={tg.description}>
              {tg.name}
            </span>
          </label>
        );
      })}
      {name && !exists && (
        <button type="button" className="btn sm outline" disabled={busy} onClick={() => onCreate(name)}>
          {t("org.tags.create", { name })}
        </button>
      )}
      <button type="button" className="btn sm" onClick={onClose}>
        {t("org.tags.close")}
      </button>
    </div>
  );
}
