#!/usr/bin/env node
// Orchestrates: mock endpoint + genisys serve → playwright → cleanup.
// Usage: node e2e/run.mjs   (env: GENISYS_BIN, SERVE_ADDR, MOCK_PORT)
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const bin = process.env.GENISYS_BIN ?? join(root, "dist", "genisys");
const serveAddr = process.env.SERVE_ADDR ?? "127.0.0.1:8720";
const mockPort = process.env.MOCK_PORT ?? "8799";

const tmp = mkdtempSync(join(tmpdir(), "genisys-e2e-"));
const cfgPath = join(tmp, "config.yaml");
writeFileSync(
  cfgPath,
  `version: 1
default_model: e2e/m
endpoints:
  - id: e2e
    base_url: http://127.0.0.1:${mockPort}/v1
    models:
      - id: m
        context_window: 8192
`,
);

const kill = (p) => {
  try {
    p?.kill("SIGKILL");
  } catch {
    /* gone */
  }
};

const mock = spawn(process.execPath, [join(root, "e2e", "mock-endpoint.mjs")], {
  env: { ...process.env, MOCK_PORT: mockPort },
  stdio: ["ignore", "pipe", "inherit"],
});
mock.stdout.resume();

const serve = spawn(
  bin,
  ["serve", "--config", cfgPath, "--db", join(tmp, "e2e.db"), "--addr", serveAddr, "--no-open"],
  { stdio: ["ignore", "pipe", "pipe"] },
);

const token = await new Promise((resolve, reject) => {
  let buf = "";
  const onData = (d) => {
    buf += d;
    const m = /token=([0-9a-f]+)/.exec(buf);
    if (m) resolve(m[1]);
  };
  serve.stdout.on("data", onData);
  serve.stderr.on("data", onData);
  serve.on("exit", (c) => reject(new Error(`serve exited early (${c}): ${buf}`)));
  setTimeout(() => reject(new Error(`serve did not start: ${buf}`)), 8000);
});

console.log(`serve up on ${serveAddr}, token acquired`);

let code = 1;
try {
  const pw = spawnSync("npx", ["playwright", "test", ...process.argv.slice(2)], {
    cwd: join(root, "ui"),
    stdio: "inherit",
    env: {
      ...process.env,
      GENISYS_BASE: `http://${serveAddr}`,
      GENISYS_TOKEN: token,
    },
  });
  code = pw.status ?? 1;
} finally {
  kill(serve);
  kill(mock);
  rmSync(tmp, { recursive: true, force: true });
}
process.exit(code);
