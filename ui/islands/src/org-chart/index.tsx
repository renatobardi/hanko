import { mountReact, readJSON, readMsgs } from "../shared/island";
import { OrgChart } from "./OrgChart";
import type { OrgNode } from "./layout";

/**
 * Props via data-*: data-nodes (JSON com as personas), data-api (ex.: /api/v1/companies/acme),
 * data-can-edit ("1" para admin/owner) e data-msgs (textos já traduzidos pelo host).
 * Sem JS o host mostra o organograma em SVG estático (os filhos do elemento).
 */
export function mount(el: HTMLElement): void {
  const nodes = readJSON<OrgNode[]>(el, "data-nodes", []);
  mountReact(el, <OrgChart nodes={nodes} canEdit={el.dataset.canEdit === "1"} apiBase={el.dataset.api ?? ""} msgs={readMsgs(el)} />);
}
