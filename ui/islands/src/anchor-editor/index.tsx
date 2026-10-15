import { mountReact, readJSON, readMsgs } from "../shared/island";
import { AnchorEditor } from "./AnchorEditor";
import type { AnchorView } from "./segments";

/** Atualiza a thread do host (fragmento #thread da própria página) sem recarregar; sem htmx, recarrega. */
function refreshThread(): void {
  const h = (window as unknown as { htmx?: { ajax: (m: string, u: string, o: object) => Promise<unknown> } }).htmx;
  if (h && document.getElementById("thread")) {
    void h.ajax("GET", location.pathname + location.search, { target: "#thread", select: "#thread", swap: "outerHTML" });
  } else {
    location.reload();
  }
}

/**
 * Props via data-*: data-doc (texto do documento, fins de linha em \n), data-anchors (JSON, já resolvidas pelo host),
 * data-target (task_description | run_summary), data-run (id da execução, só no resumo), data-task (chave da task),
 * data-api, data-can-comment ("1" para member+) e data-msgs. Sem JS o host mostra o texto renderizado (filhos).
 */
export function mount(el: HTMLElement): void {
  const d = el.dataset;
  mountReact(
    el,
    <AnchorEditor
      doc={d.doc ?? ""}
      anchors={readJSON<AnchorView[]>(el, "data-anchors", [])}
      target={d.target === "run_summary" ? "run_summary" : "task_description"}
      runId={d.run || undefined}
      taskKey={d.task ?? ""}
      apiBase={d.api ?? ""}
      canComment={d.canComment === "1"}
      msgs={readMsgs(el)}
      onPosted={refreshThread}
    />,
  );
}
