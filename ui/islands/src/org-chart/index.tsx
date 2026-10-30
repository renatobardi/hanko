import { mountReact, readJSON, readMsgs } from "../shared/island";
import { OrgChart } from "./OrgChart";
import type { OrgNode } from "./layout";
import type { OrgFilter, TagInfo } from "./filter";

/**
 * Props via data-*: data-nodes (JSON com as personas), data-api (ex.: /api/v1/companies/acme),
 * data-can-edit ("1" para admin/owner), data-can-tag ("1" para member ou mais), data-tags (cadastro de tags), data-filter
 * (filtro inicial, da URL ou do cookie hanko_org), data-tags-href (aba Tags) e data-msgs (textos já traduzidos pelo host).
 * Sem JS o host mostra o organograma em SVG estático (os filhos do elemento).
 */
export function mount(el: HTMLElement): void {
  const nodes = readJSON<OrgNode[]>(el, "data-nodes", []);
  const tags = readJSON<TagInfo[]>(el, "data-tags", []);
  const filter = readJSON<Partial<OrgFilter>>(el, "data-filter", {});
  mountReact(
    el,
    <OrgChart
      nodes={nodes}
      canEdit={el.dataset.canEdit === "1"}
      apiBase={el.dataset.api ?? ""}
      msgs={readMsgs(el)}
      tags={tags}
      filter={filter}
      canTag={el.dataset.canTag === "1"}
      tagsHref={el.dataset.tagsHref}
    />,
  );
}
