import type { ReactNode } from "react";
import { createRoot } from "react-dom/client";

/** Textos já traduzidos pelo host (data-msgs, JSON). Não há catálogo no JS: chave ausente cai na própria chave. */
export type Msgs = Record<string, string>;
export type T = (key: string, vars?: Record<string, string | number>) => string;

export function readMsgs(el: HTMLElement): Msgs {
  try {
    const v = JSON.parse(el.dataset.msgs ?? "{}") as unknown;
    return v && typeof v === "object" ? (v as Msgs) : {};
  } catch {
    return {};
  }
}

export function makeT(msgs: Msgs): T {
  return (key, vars) => {
    let s = msgs[key] ?? key;
    if (vars) for (const [k, v] of Object.entries(vars)) s = s.split(`{${k}}`).join(String(v));
    return s;
  };
}

/** Lê um data-* JSON; devolve o padrão se faltar ou estiver quebrado. */
export function readJSON<V>(el: HTMLElement, name: string, fallback: V): V {
  const raw = el.getAttribute(name);
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as V;
  } catch {
    return fallback;
  }
}

/** Monta a ilha no elemento. Os filhos do host (versão htmx) ficam como fallback até o React assumir. */
export function mountReact(el: HTMLElement, node: ReactNode): void {
  el.replaceChildren();
  createRoot(el).render(node);
  el.dataset.islandMounted = "1";
}
