import type { T } from "../shared/island";
import type { OrgFilter, TagInfo } from "./filter";

export interface FilterBarProps {
  tags: readonly TagInfo[];
  filter: OrgFilter;
  onChange: (f: OrgFilter) => void;
  /** Níveis da árvore inteira (1 = topo). */
  levels: number;
  tagsHref?: string;
  t: T;
}

const toggle = <V,>(list: readonly V[], v: V): V[] => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);

/** Filtros do organograma (ADR-0087 item 7): tags (OU ou E), sem tag, arquivadas, níveis, profundidade e modo. */
export function FilterBar({ tags, filter, onChange, levels, tagsHref, t }: FilterBarProps) {
  const set = (patch: Partial<OrgFilter>) => onChange({ ...filter, ...patch });
  const listed = tags.filter((tg) => filter.archived || !tg.archived);
  const levelList = Array.from({ length: Math.max(levels, 1) }, (_, i) => i + 1);
  const tagCount = filter.tags.length + (filter.untagged ? 1 : 0);
  return (
    <div className="org-filters" role="group" aria-label={t("org.filter.label")}>
      <details>
        <summary className="btn sm outline">{tagCount > 0 ? t("org.filter.tags.n", { n: tagCount }) : t("org.filter.tags")}</summary>
        <div className="org-pop">
          {listed.length === 0 && <span className="muted text-sm">{t("org.filter.notags")}</span>}
          {listed.map((tg) => (
            <label key={tg.id}>
              <input type="checkbox" checked={filter.tags.includes(tg.id)} onChange={() => set({ tags: toggle(filter.tags, tg.id) })} />
              <span className={`tag-chip tag-c-${tg.color}${tg.archived ? " archived" : ""}`} title={tg.description}>
                {tg.name}
              </span>
            </label>
          ))}
          <label>
            <input type="checkbox" checked={filter.untagged} onChange={() => set({ untagged: !filter.untagged })} />
            {t("org.filter.untagged")}
          </label>
          <label>
            <input type="checkbox" checked={filter.all} onChange={() => set({ all: !filter.all })} />
            {t("org.filter.all")}
          </label>
          <label>
            <input
              type="checkbox"
              checked={filter.archived}
              onChange={() =>
                set({ archived: !filter.archived, tags: filter.archived ? filter.tags.filter((id) => !tags.find((tg) => tg.id === id)?.archived) : filter.tags })
              }
            />
            {t("org.filter.archived")}
          </label>
          {tagsHref && (
            <a className="link-u text-sm" href={tagsHref}>
              {t("org.filter.manage")}
            </a>
          )}
        </div>
      </details>
      <details>
        <summary className="btn sm outline">{filter.levels.length > 0 ? t("org.filter.levels.n", { n: filter.levels.join(", ") }) : t("org.filter.levels")}</summary>
        <div className="org-pop">
          {levelList.map((n) => (
            <label key={n}>
              <input type="checkbox" checked={filter.levels.includes(n)} onChange={() => set({ levels: toggle(filter.levels, n).sort((a, b) => a - b) })} />
              {t("org.filter.level", { n })}
            </label>
          ))}
        </div>
      </details>
      <label className="text-sm">
        {t("org.filter.depth")}{" "}
        <select className="select sm" value={filter.maxDepth} onChange={(e) => set({ maxDepth: Number(e.target.value) })}>
          <option value={0}>{t("org.filter.depth.all")}</option>
          {levelList.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
      <label className="text-sm">
        {t("org.filter.mode")}{" "}
        <select className="select sm" value={filter.mode} onChange={(e) => set({ mode: e.target.value === "prune" ? "prune" : "highlight" })}>
          <option value="highlight">{t("org.filter.mode.highlight")}</option>
          <option value="prune">{t("org.filter.mode.prune")}</option>
        </select>
      </label>
      {(filter.tags.length > 0 || filter.untagged || filter.levels.length > 0 || filter.maxDepth > 0) && (
        <button type="button" className="btn sm outline" onClick={() => onChange({ ...filter, tags: [], untagged: false, all: false, levels: [], maxDepth: 0 })}>
          {t("org.filter.clear")}
        </button>
      )}
    </div>
  );
}
