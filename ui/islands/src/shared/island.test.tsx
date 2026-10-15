import { act } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mountReact } from "./island";

function Boom(): never {
  throw new Error("quebrou");
}

describe("mountReact", () => {
  afterEach(() => vi.restoreAllMocks());

  it("troca o HTML do host pela ilha", async () => {
    const el = document.createElement("div");
    el.innerHTML = "<p>versão do servidor</p>";
    await act(async () => mountReact(el, <span>ilha</span>));
    expect(el.textContent).toBe("ilha");
    expect(el.dataset.islandMounted).toBe("1");
  });

  it("se a ilha quebra ao renderizar, restaura o HTML original do host", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.useFakeTimers();
    const el = document.createElement("div");
    el.innerHTML = '<ul class="fallback"><li>um</li><li>dois</li></ul>';
    const before = el.innerHTML;
    const li = el.querySelector("li");
    await act(async () => mountReact(el, <Boom />));
    await act(async () => {
      vi.runAllTimers();
    });
    vi.useRealTimers();
    expect(el.innerHTML).toBe(before);
    expect(el.querySelector("li")).toBe(li); // os mesmos nós: listeners do htmx continuam valendo
    expect(el.dataset.islandMounted).toBeUndefined();
    expect(el.dataset.islandFailed).toBe("1");
  });

  it("a falha de uma ilha não mexe em outra já montada", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.useFakeTimers();
    let blow = false;
    function Late() {
      if (blow) throw new Error("tarde");
      return <b>ok</b>;
    }
    const el = document.createElement("div");
    el.innerHTML = "<i>fallback</i>";
    await act(async () => mountReact(el, <Late />));
    expect(el.textContent).toBe("ok");
    blow = true;
    const el2 = document.createElement("div");
    el2.innerHTML = "<i>fallback 2</i>";
    await act(async () => mountReact(el2, <Late />));
    await act(async () => {
      vi.runAllTimers();
    });
    vi.useRealTimers();
    expect(el2.innerHTML).toBe("<i>fallback 2</i>");
    expect(el.textContent).toBe("ok");
  });
});
