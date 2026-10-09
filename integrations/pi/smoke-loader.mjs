// Load the real Pi extension in an isolated RPC session without calling a model.
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const [piBinary, karteBinary] = process.argv.slice(2);
if (!piBinary || !karteBinary) throw new Error("Usage: node smoke-loader.mjs PI_BINARY KARTE_BINARY");
const root = await mkdtemp(join(tmpdir(), "karte-pi-loader-"));
let child;
try {
  const data = join(root, "data"); await mkdir(data);
  const initialized = spawnSync(resolve(karteBinary), ["--root", data, "init"], { windowsHide: true, encoding: "utf8" });
  assert.equal(initialized.status, 0, initialized.stderr);
  child = spawn(resolve(piBinary), ["--mode", "rpc", "--offline", "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve", "-e", fileURLToPath(new URL("./karte-worklog.ts", import.meta.url))], { cwd: root, windowsHide: true, stdio: ["pipe", "pipe", "pipe"], env: { ...process.env, PI_CODING_AGENT_DIR: join(root, "agent"), PI_OFFLINE: "1", KARTE_WORKLOG_ROOT: data, KARTE_WORKLOG_BIN: resolve(karteBinary) } });
  const commands = await new Promise((ok, fail) => {
    let buffer = "", errors = "";
    const timer = setTimeout(() => fail(new Error(`Pi loader timed out: ${errors.slice(0, 2000)}`)), 15000);
    child.on("error", (error) => { clearTimeout(timer); fail(error); });
    child.stderr.on("data", (part) => { errors += part.toString(); });
    child.stdout.on("data", (part) => {
      buffer += part.toString();
      let index;
      while ((index = buffer.indexOf("\n")) >= 0) {
        const raw = buffer.slice(0, index); buffer = buffer.slice(index + 1);
        try { const frame = JSON.parse(raw); if (frame.id === "commands") { clearTimeout(timer); frame.success ? ok(frame.data.commands) : fail(new Error(JSON.stringify(frame))); } } catch { /* non-protocol diagnostics are retained by Pi */ }
      }
    });
    child.stdin.write(JSON.stringify({ id: "commands", type: "get_commands" }) + "\n");
  });
  assert.ok(commands.some((c) => c.name === "karte-flush"), "Karte extension command missing");
  console.log("PASS: Pi 0.86 extension loader registered Karte hooks and tools (no model request)");
} finally {
  if (child && child.exitCode === null) { child.kill(); await new Promise((done) => child.once("close", done)); }
  await rm(root, { recursive: true, force: true });
}
