import { Component, type ReactNode } from "react";
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

/**
 * Pega erro de renderização da ilha e chama onError (uma vez). Renderiza nada: quem restaura o HTML do host é o
 * mountReact, depois de desmontar a raiz (desmontar de dentro do commit que falhou não é seguro).
 */
class IslandBoundary extends Component<{ onError: (e: unknown) => void; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(error: unknown) {
    this.props.onError(error);
  }
  render() {
    return this.state.failed ? null : this.props.children;
  }
}

/**
 * Monta a ilha no elemento. Os filhos do host (versão htmx/SVG) ficam guardados e voltam, intactos, se a ilha
 * quebrar ao renderizar: a tela nunca fica em branco por causa do JS (ADR-0026, "sem JS" é o fallback).
 */
export function mountReact(el: HTMLElement, node: ReactNode): void {
  const original = Array.from(el.childNodes); // os próprios nós (com listeners do htmx), não uma cópia do HTML
  el.replaceChildren();
  const root = createRoot(el);
  let restored = false;
  const restore = (error: unknown) => {
    if (restored) return;
    restored = true;
    console.error("hanko: a ilha falhou ao renderizar; restaurando o HTML do servidor", error);
    // fora do commit com erro: primeiro desmonta, depois devolve o HTML original
    setTimeout(() => {
      root.unmount();
      el.replaceChildren(...original);
      delete el.dataset.islandMounted;
      el.dataset.islandFailed = "1";
    }, 0);
  };
  root.render(<IslandBoundary onError={restore}>{node}</IslandBoundary>);
  el.dataset.islandMounted = "1";
}
