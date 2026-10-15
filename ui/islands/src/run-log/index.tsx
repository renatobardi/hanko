import { mountReact, readMsgs } from "../shared/island";
import { RunLog } from "./RunLog";

/**
 * Props via data-*: data-api (ex.: /api/v1/companies/acme), data-run (uuid), data-live ("1" enquanto a execução
 * roda), data-tz, data-lang e data-msgs (JSON com os textos já traduzidos pelo host).
 */
export function mount(el: HTMLElement): void {
  const d = el.dataset;
  mountReact(
    el,
    <RunLog apiBase={d.api ?? ""} runId={d.run ?? ""} live={d.live === "1"} tz={d.tz ?? ""} lang={d.lang ?? ""} msgs={readMsgs(el)} />,
  );
}
