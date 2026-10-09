import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { Worklog, installHooks, cliTransport } from "./worklog.mjs";

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "karte-pi-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const delivered = [], notifications = [];
  let offline = false;
  const transport = async (command, input, observer) => {
    if (offline) throw new Error("offline");
    delivered.push({ command, input, observer });
    if (command === "daily") return { date: "2026-10-09" };
    return { task_id: input.task_id, sequence: 1, recorded: true, views_ready: true };
  };
  const options = { root, spool: join(root, "spool"), sessionId: "one", transport, notify: (...args) => notifications.push(args) };
  return { root, delivered, notifications, options, logger: new Worklog(options), setOffline: (value) => { offline = value; } };
}

test("offline events persist and replay with their original IDs", async (t) => {
  const f = await fixture(t); f.setOffline(true);
  await f.logger.begin(); await f.logger.observe("tool failed", "failure", "check evidence");
  const saved = JSON.parse(await readFile(f.logger.file, "utf8"));
  assert.equal(saved.queue.length, 2); assert.match(f.notifications.at(-1)[0], /再送待ち/);
  f.setOffline(false);
  const restarted = new Worklog(f.options); assert.equal(await restarted.flush(), 0);
  assert.deepEqual(f.delivered.map((e) => e.input.event_id), saved.queue.map((e) => e.input.event_id));
  assert.equal(JSON.parse(await readFile(f.logger.file, "utf8")).queue.length, 0);
});

test("Pi hooks capture failure without raw prompts or tool payloads", async (t) => {
  const f = await fixture(t), handlers = new Map();
  installHooks({ on: (name, fn) => handlers.set(name, fn) }, async () => f.logger);
  await handlers.get("before_agent_start")({ prompt: "secret prompt" });
  await handlers.get("agent_start")({});
  await handlers.get("tool_execution_start")({ toolName: "bash", toolCallId: "b1", args: { command: "secret command" } });
  await handlers.get("tool_execution_end")({ toolName: "bash", toolCallId: "b1", isError: true, result: "secret result" });
  await handlers.get("agent_settled")({});
  const raw = JSON.stringify(f.delivered);
  for (const secret of ["secret prompt", "secret command", "secret result"]) assert.ok(!raw.includes(secret));
  assert.ok(f.delivered.some((e) => e.input.kind === "failure" && e.observer));
  assert.equal(f.delivered.at(-1).input.outcome, "paused");
  assert.ok(!f.delivered.some((e) => e.input.kind === "verification" || e.input.outcome === "completed"));
  const count = f.delivered.length;
  await handlers.get("tool_execution_end")({ toolName: "karte_work_note", toolCallId: "own", isError: false });
  assert.equal(f.delivered.length, count);
});

test("model reports retain their source and invalid events cannot poison the queue", async (t) => {
  const f = await fixture(t); await f.logger.begin();
  await assert.rejects(f.logger.append({ kind: "finish", text: "bad" }, false), /outcome/);
  await f.logger.append({ kind: "verification", text: "go test succeeded", evidence: [{ reference: "run:1" }] }, false);
  assert.equal(f.delivered.at(-1).observer, false);
  await f.logger.append({ kind: "finish", outcome: "completed", text: "work ended" }, false);
  await f.logger.settled(); assert.equal(f.delivered.at(-1).input.kind, "note");
  assert.equal(await f.logger.flush(), 0);
});

test("parallel observations serialize and spools cannot cross roots", async (t) => {
  const f = await fixture(t); await f.logger.begin();
  await Promise.all(Array.from({ length: 8 }, (_, i) => f.logger.observe(`tool ${i}`, "tool")));
  assert.equal(f.delivered.length, 9);
  const other = new Worklog({ ...f.options, root: join(f.root, "other") });
  await assert.rejects(other.flush(), /mismatched/);
});

test("missing acknowledgements preserve the pending record", async (t) => {
  const f = await fixture(t);
  f.logger.transport = async (_command, input) => ({ task_id: input.task_id, recorded: true, views_ready: false, sequence: 1 });
  await f.logger.begin(); assert.equal(await f.logger.flush(), 1);
  assert.equal(JSON.parse(await readFile(f.logger.file, "utf8")).queue.length, 1);
});

test("compiled Karte CLI accepts Pi events and creates searchable daily reports", { skip: !process.env.KARTE_TEST_BIN }, async (t) => {
  const f = await fixture(t), binary = process.env.KARTE_TEST_BIN;
  const init = spawnSync(binary, ["--root", f.root, "init"], { encoding: "utf8", windowsHide: true });
  assert.equal(init.status, 0, init.stderr);
  const logger = new Worklog({ ...f.options, transport: cliTransport(binary, f.root) });
  const id = await logger.begin(); await logger.observe("host saw isError=true", "failure", "retry search with evidence");
  await logger.append({ kind: "note", text: "原因の候補: rate limit．未検証．" }, false);
  await logger.settled(); await logger.tick();
  const result = await logger.query("read", { task_id: id });
  assert.equal(result.task.status, "paused");
  assert.ok(result.events.some((e) => e.kind === "failure" && e.origin === "host_observation"));
  assert.ok(result.events.some((e) => e.origin === "model_report"));
  const search = await logger.query("search", { query: "isError", limit: 10 });
  assert.equal(search.results[0].task_id, id);
});
