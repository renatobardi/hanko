import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AnchorEditor } from "./AnchorEditor";
import { buildSegments, normalizeSelection, type AnchorView } from "./segments";

const doc = "Implemente o login.\nUse OAuth com PKCE — é mais seguro 😀 e simples.\nFim.";
const cp = (s: string) => Array.from(doc.slice(0, doc.indexOf(s))).length;
const anchor = (id: string, quote: string, over: Partial<AnchorView> = {}): AnchorView => ({
  id,
  quote,
  start: cp(quote),
  end: cp(quote) + Array.from(quote).length,
  status: "exact",
  author: "Você",
  when: "15/10 10:00",
  body: `comentário ${id}`,
  ...over,
});

describe("segmentos", () => {
  it("divide por code points (emoji conta 1) e ignora âncoras perdidas", () => {
    const segs = buildSegments(doc, [anchor("a", "PKCE"), anchor("b", "simples."), anchor("x", "sumiu", { status: "lost", start: 0, end: 5 })]);
    expect(segs.map((s) => s.text).join("")).toBe(doc);
    expect(segs.filter((s) => s.ids.length).map((s) => s.text)).toEqual(["PKCE", "simples."]);
  });

  it("sobreposição gera trechos com mais de um id", () => {
    const segs = buildSegments("abcdef", [
      { ...anchor("a", "x"), start: 0, end: 4 },
      { ...anchor("b", "x"), start: 2, end: 6 },
    ]);
    expect(segs.map((s) => [s.text, s.ids.join("+")])).toEqual([
      ["ab", "a"],
      ["cd", "a+b"],
      ["ef", "b"],
    ]);
  });

  it("normalizeSelection tira espaços das pontas e converte para code points", () => {
    const i = doc.indexOf("simples");
    const s = normalizeSelection(doc, i - 2, i + "simples. ".length)!;
    expect(s.quote).toBe("e simples.");
    expect(s.start).toBe(cp("e simples."));
    expect(s.end).toBe(s.start + 10);
    expect(normalizeSelection(doc, 3, 3)).toBeNull();
    expect(normalizeSelection("a   b", 1, 4)).toBeNull();
    expect(normalizeSelection("x".repeat(3000), 0, 3000)).toBeNull();
  });
});

const msgs = {
  "anchor.comment": "Comentar neste trecho",
  "anchor.placeholder": "Escreva o comentário",
  "anchor.send": "Enviar",
  "anchor.cancel": "Cancelar",
  "anchor.lost": "âncora perdida",
  "anchor.lost.title": "Comentários com âncora perdida",
  "anchor.sent": "Comentário enviado.",
  "anchor.you": "Você",
};
const base = { doc, target: "task_description" as const, taskKey: "HNK-1", apiBase: "/api/v1/companies/acme", msgs };

/** Seleciona `text` dentro do documento renderizado (atravessando nós quando há marcas). */
function select(text: string) {
  const root = document.querySelector(".anchor-doc")!;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n as Text);
  const full = nodes.map((n) => n.data).join("");
  const start = full.indexOf(text);
  const end = start + text.length;
  const locate = (pos: number): [Text, number] => {
    let acc = 0;
    for (const n of nodes) {
      if (pos <= acc + n.data.length) return [n, pos - acc];
      acc += n.data.length;
    }
    throw new Error("fora do documento");
  };
  const range = document.createRange();
  range.setStart(...locate(start));
  range.setEnd(...locate(end));
  const sel = window.getSelection()!;
  sel.removeAllRanges();
  sel.addRange(range);
  act(() => {
    document.dispatchEvent(new Event("selectionchange"));
  });
}

describe("AnchorEditor", () => {
  it("destaca os trechos ancorados e mostra o comentário ao clicar", () => {
    render(<AnchorEditor {...base} anchors={[anchor("a", "PKCE")]} canComment={false} />);
    const mark = document.querySelector("mark.anchor-mark")!;
    expect(mark.textContent).toBe("PKCE");
    expect(screen.queryByText("comentário a")).toBeNull();
    fireEvent.click(mark);
    expect(screen.getByText("comentário a")).toBeTruthy();
    expect(document.querySelector(".anchor-card blockquote")!.textContent).toBe("PKCE");
  });

  it("lista a âncora perdida à parte, com a citação", () => {
    render(<AnchorEditor {...base} anchors={[anchor("z", "trecho removido", { status: "lost", start: 0, end: 0 })]} canComment={false} />);
    expect(document.querySelector("mark")).toBeNull();
    expect(screen.getByText("Comentários com âncora perdida")).toBeTruthy();
    expect(screen.getByText("trecho removido")).toBeTruthy();
    expect(screen.getByText("âncora perdida")).toBeTruthy();
  });

  it("selecionar texto abre o comentário e envia a âncora em code points", async () => {
    const fetchImpl = vi.fn(async (_u: string, init?: RequestInit) => {
      const sent = JSON.parse(init!.body as string) as { anchor: { quote: string; start: number; end: number } };
      return new Response(JSON.stringify({ id: "c1", body: "ok", anchor: sent.anchor, anchor_state: { status: "exact", start: sent.anchor.start, end: sent.anchor.end } }), { status: 201 });
    });
    const onPosted = vi.fn();
    render(<AnchorEditor {...base} anchors={[]} canComment fetchImpl={fetchImpl} onPosted={onPosted} />);
    select("é mais seguro 😀 e simples.");
    fireEvent.click(await screen.findByRole("button", { name: "Comentar neste trecho" }));
    expect(document.querySelector(".anchor-quote")!.textContent).toBe("é mais seguro 😀 e simples.");
    fireEvent.change(screen.getByLabelText("Escreva o comentário"), { target: { value: "  Por quê?  " } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Enviar" }));
    });
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(1));
    const [url, init] = fetchImpl.mock.calls[0]!;
    expect(url).toBe("/api/v1/companies/acme/tasks/HNK-1/comments");
    const body = JSON.parse(init!.body as string);
    expect(body.body).toBe("Por quê?");
    expect(body.anchor).toEqual({ target: "task_description", quote: "é mais seguro 😀 e simples.", start: cp("é mais"), end: cp("é mais") + 26 });
    await waitFor(() => expect(onPosted).toHaveBeenCalled());
    // o trecho passa a ficar destacado
    await waitFor(() => expect(document.querySelector("mark.anchor-mark")?.textContent).toBe("é mais seguro 😀 e simples."));
    expect(screen.getByText("Comentário enviado.")).toBeTruthy();
  });

  it("no resumo de uma execução envia run_id", async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ id: "c1", body: "x" }), { status: 201 }));
    render(<AnchorEditor {...base} target="run_summary" runId="run-9" anchors={[]} canComment fetchImpl={fetchImpl} />);
    select("Fim.");
    fireEvent.click(await screen.findByRole("button", { name: "Comentar neste trecho" }));
    fireEvent.change(screen.getByLabelText("Escreva o comentário"), { target: { value: "ok" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Enviar" }));
    });
    const init = (fetchImpl.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect(JSON.parse(init.body as string).anchor).toMatchObject({ target: "run_summary", run_id: "run-9", quote: "Fim." });
  });

  it("mostra o erro da API e mantém o formulário", async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ title: "Unprocessable", detail: "text not found in the target document" }), { status: 422 }));
    render(<AnchorEditor {...base} anchors={[]} canComment fetchImpl={fetchImpl} />);
    select("Fim.");
    fireEvent.click(await screen.findByRole("button", { name: "Comentar neste trecho" }));
    fireEvent.change(screen.getByLabelText("Escreva o comentário"), { target: { value: "ok" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Enviar" }));
    });
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("text not found"));
    expect(screen.getByLabelText("Escreva o comentário")).toBeTruthy();
  });

  it("viewer não vê o botão de comentar", () => {
    render(<AnchorEditor {...base} anchors={[]} canComment={false} />);
    select("Fim.");
    expect(screen.queryByRole("button", { name: "Comentar neste trecho" })).toBeNull();
  });

  it("seleção fora do documento é ignorada", () => {
    render(
      <div>
        <p id="outside">fora</p>
        <AnchorEditor {...base} anchors={[]} canComment />
      </div>,
    );
    const r = document.createRange();
    r.selectNodeContents(document.getElementById("outside")!);
    window.getSelection()!.removeAllRanges();
    window.getSelection()!.addRange(r);
    act(() => {
      document.dispatchEvent(new Event("selectionchange"));
    });
    expect(screen.queryByRole("button", { name: "Comentar neste trecho" })).toBeNull();
  });
});
