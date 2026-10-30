import { describe, expect, it } from "vitest";
import { applyFilter, emptyFilter, filterToQuery, levelsOf, persistFilter, sanitize, type OrgFilter, type TagInfo } from "./filter";
import type { OrgNode } from "./layout";

const n = (id: string, parent = "", tags: string[] = []): OrgNode => ({
  id, slug: id, name: id, title: "", parent, status: "", cls: "idle", meta: "", href: "", gate: false, initials: "", tags,
});

// ceo(back) <- cto <- eng(back,test), qa(test) ; solo
const tree = (): OrgNode[] => [n("ceo", "", ["back"]), n("cto", "ceo"), n("eng", "cto", ["back", "test"]), n("qa", "cto", ["test"]), n("solo")];
const f = (over: Partial<OrgFilter>): OrgFilter => ({ ...emptyFilter, ...over });
const ids = (s: Set<string>) => [...s].sort();

const catalog: TagInfo[] = [
  { id: "back", name: "Backend", color: "blue", description: "", archived: false },
  { id: "test", name: "Testes", color: "green", description: "", archived: false },
  { id: "old", name: "Legado", color: "gray", description: "", archived: true },
];

describe("recorte do organograma (ADR-0087)", () => {
  it("nível 1 é o topo; o superior que não está na árvore vira topo", () => {
    const l = levelsOf([...tree(), n("orfa", "sumiu")]);
    expect([l.get("ceo"), l.get("cto"), l.get("eng"), l.get("solo"), l.get("orfa")]).toEqual([1, 2, 3, 1, 1]);
  });

  it("sem critério: tudo visível, nada esmaecido", () => {
    const s = applyFilter(tree(), emptyFilter);
    expect(s.active).toBe(false);
    expect(s.visible.size).toBe(5);
    expect(s.dim.size).toBe(0);
  });

  it("Destacar: OU por padrão, esmaece quem está fora", () => {
    const s = applyFilter(tree(), f({ tags: ["back", "test"] }));
    expect(s.matched).toBe(3);
    expect(ids(s.visible)).toEqual(["ceo", "cto", "eng", "qa", "solo"]);
    expect(ids(s.dim)).toEqual(["cto", "solo"]);
  });

  it("E exige todas as tags; sem tag entra por OU", () => {
    expect(applyFilter(tree(), f({ tags: ["back", "test"], all: true })).matched).toBe(1);
    expect(ids(applyFilter(tree(), f({ untagged: true })).dim)).toEqual(["ceo", "eng", "qa"]);
    expect(applyFilter(tree(), f({ untagged: true, tags: ["test"] })).matched).toBe(4);
  });

  it("Podar mantém o recorte e os superiores até o topo, esmaecidos como caminho", () => {
    const s = applyFilter(tree(), f({ tags: ["test"], mode: "prune" }));
    expect(ids(s.visible)).toEqual(["ceo", "cto", "eng", "qa"]);
    expect(ids(s.path)).toEqual(["ceo", "cto"]);
    expect(ids(s.dim)).toEqual(["ceo", "cto"]);
  });

  it("tag e nível por E; profundidade corta antes", () => {
    expect(applyFilter(tree(), f({ tags: ["back"], levels: [3] })).matched).toBe(1);
    const s = applyFilter(tree(), f({ maxDepth: 2 }));
    expect(s.active).toBe(false);
    expect(ids(s.visible)).toEqual(["ceo", "cto", "solo"]);
    const empty = applyFilter(tree(), f({ tags: ["test"], maxDepth: 2 }));
    expect(empty.active && empty.matched === 0).toBe(true);
  });

  it("sanitize tira tag desconhecida e arquivada sem 'mostrar arquivadas'", () => {
    expect(sanitize({ tags: ["back", "nada", "old"], levels: [0, 2, 2], maxDepth: -1, mode: "x" as never }, catalog)).toEqual(
      f({ tags: ["back"], levels: [2] }),
    );
    expect(sanitize({ tags: ["old"], archived: true }, catalog).tags).toEqual(["old"]);
  });

  it("URL e cookie usam os parâmetros do host", () => {
    const q = filterToQuery(f({ tags: ["a", "b"], all: true, untagged: true, archived: true, levels: [1, 3], maxDepth: 2, mode: "prune" }));
    expect(q.toString()).toBe("tag=a%2Cb&todas=1&semtag=1&arquivadas=1&nivel=1%2C3&prof=2&modo=podar");
    expect(filterToQuery(emptyFilter).toString()).toBe("");
  });

  it("persistFilter grava a URL sem recarregar e o cookie hanko_org que o host decodifica", () => {
    window.history.replaceState(null, "", "/c/acme/organograma?x=1&tag=velha");
    persistFilter(f({ tags: ["a"], mode: "prune" }));
    expect(window.location.search).toBe("?x=1&tag=a&modo=podar");
    const raw = document.cookie.split("; ").find((c) => c.startsWith("hanko_org="))!.slice("hanko_org=".length);
    expect(decodeURIComponent(raw)).toBe("tag=a&modo=podar");
  });
});
