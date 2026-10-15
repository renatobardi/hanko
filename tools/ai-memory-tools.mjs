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

// Consulta de amostra (confere o formato da resposta): -e Q=palavra -e WS=<slug-da-empresa> -e PJ=default
if (process.env.Q && process.env.WS) {
  const r = await post(headers, {
    jsonrpc: "2.0", id: 3, method: "tools/call",
    params: { name: "memory_query", arguments: { query: `"${process.env.Q}"`, workspace: process.env.WS, project: process.env.PJ || "default", limit: 3 } },
  });
  console.log("\n--- memory_query ---\n" + (await r.text()).slice(0, 4000));
}
