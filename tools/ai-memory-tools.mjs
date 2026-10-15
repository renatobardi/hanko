// Lista as tools do ai-memory (MCP tools/list) e o schema de entrada das que o Hanko usa. Roda DENTRO do container do Hanko:
//   ssh oute-server 'lxc exec hanko-prd -- docker compose -f /opt/app/docker-compose.yml exec -T hanko node -' < tools/ai-memory-tools.mjs
const url = (process.env.HANKO_MEMORY_URL || "http://ai-memory:49374") + "/mcp";
const base = {
  Authorization: "Bearer " + (process.env.HANKO_MEMORY_TOKEN || ""),
  "Content-Type": "application/json",
  Accept: "application/json, text/event-stream",
};
const post = (headers, body) => fetch(url, { method: "POST", headers, body: JSON.stringify(body) });

const init = await post(base, {
  jsonrpc: "2.0", id: 1, method: "initialize",
  params: { protocolVersion: "2025-03-26", capabilities: {}, clientInfo: { name: "hanko-check", version: "1" } },
});
const sid = init.headers.get("mcp-session-id");
await init.text();
const headers = sid ? { ...base, "Mcp-Session-Id": sid } : base;
await post(headers, { jsonrpc: "2.0", method: "notifications/initialized" });
const res = await post(headers, { jsonrpc: "2.0", id: 2, method: "tools/list" });
const text = await res.text();
// resposta JSON ou SSE ("data: {...}")
const line = text.split("\n").find((l) => l.startsWith("data:")) ?? text;
const json = JSON.parse(line.replace(/^data:\s*/, ""));
const want = new Set(["memory_query", "memory_read_page", "memory_recent", "memory_delete_page"]);
for (const t of json.result?.tools ?? []) {
  console.log(t.name);
  if (want.has(t.name)) console.log(JSON.stringify(t.inputSchema));
}
if (!json.result) console.log(text.slice(0, 2000));

// Amostra do formato das respostas (com WS = slug da empresa): memory_recent e, se houver Q, memory_query.
//   -e WS=<slug> -e PJ=default -e Q=palavra
if (process.env.WS) {
  const scope = { workspace: process.env.WS, project: process.env.PJ || "default" };
  const call = async (id, name, args) => {
    const r = await post(headers, { jsonrpc: "2.0", id, method: "tools/call", params: { name, arguments: args } });
    console.log(`\n--- ${name} ---\n` + (await r.text()).slice(0, 3000));
  };
  await call(3, "memory_recent", { ...scope, limit: 3 });
  if (process.env.Q) await call(4, "memory_query", { ...scope, query: `"${process.env.Q}"`, limit: 3 });
}
