// Tests for npm/cli/bin/tello.js. Fake binaries are small Node scripts that the
// launcher runs through TELLO_BINARY_PATH.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const launcher = fileURLToPath(new URL("../cli/bin/tello.js", import.meta.url));
const posixOnly = { skip: process.platform === "win32" && "needs POSIX signals", timeout: 20_000 };

function tempDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tello-launcher-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// An executable Node script. Fakes that wait for the test give up after 15s so a
// failed test never leaves them running.
function fakeBinary(dir, body) {
  const file = path.join(dir, "fake-tello");
  fs.writeFileSync(file, `#!${process.execPath}\nsetTimeout(() => process.exit(98), 15000).unref();\n${body}\n`, {
    mode: 0o755,
  });
  return file;
}

function startLauncher(t, binary, args = []) {
  const child = spawn(process.execPath, [launcher, ...args], {
    env: { ...process.env, TELLO_BINARY_PATH: binary },
    stdio: ["pipe", "pipe", "pipe"],
  });
  t.after(() => {
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
  });
  const run = { child, stdout: "", stderr: "" };
  child.stdout.setEncoding("utf8").on("data", (chunk) => (run.stdout += chunk));
  child.stderr.setEncoding("utf8").on("data", (chunk) => (run.stderr += chunk));
  run.exited = new Promise((resolve) => child.on("close", (code, signal) => resolve({ code, signal })));
  run.waitForStdout = (text) =>
    new Promise((resolve) => {
      const check = () => {
        if (!run.stdout.includes(text)) return;
        child.stdout.off("data", check);
        resolve();
      };
      child.stdout.on("data", check);
      check();
    });
  return run;
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

test("passes arguments and stdin through and returns the binary's exit code", posixOnly, async (t) => {
  const binary = fakeBinary(
    tempDir(t),
    `let input = "";
process.stdin.setEncoding("utf8").on("data", (d) => (input += d)).on("end", () => {
  console.log(JSON.stringify({ args: process.argv.slice(2), input }));
  process.exit(7);
});`,
  );
  const run = startLauncher(t, binary, ["call", "create", "--to", "+82 10 1234", "--json"]);
  run.child.stdin.end("hello\n");

  assert.deepEqual(await run.exited, { code: 7, signal: null });
  assert.deepEqual(JSON.parse(run.stdout), { args: ["call", "create", "--to", "+82 10 1234", "--json"], input: "hello\n" });
});

for (const signal of ["SIGTERM", "SIGHUP"]) {
  test(`forwards ${signal} to the binary`, posixOnly, async (t) => {
    const binary = fakeBinary(
      tempDir(t),
      `process.on(${JSON.stringify(signal)}, () => { console.log("got ${signal}"); process.exit(42); });
console.log("ready");
setInterval(() => {}, 1000);`,
    );
    const run = startLauncher(t, binary);
    await run.waitForStdout("ready");

    run.child.kill(signal);

    assert.deepEqual(await run.exited, { code: 42, signal: null });
    assert.match(run.stdout, new RegExp(`got ${signal}`));
  });
}

test("swallows SIGINT sent to the launcher alone; the binary keeps running", posixOnly, async (t) => {
  const binary = fakeBinary(
    tempDir(t),
    `process.on("SIGINT", () => { console.log("got SIGINT"); process.exit(99); });
process.stdin.once("data", () => process.exit(5));
console.log("ready");`,
  );
  const run = startLauncher(t, binary);
  await run.waitForStdout("ready");

  run.child.kill("SIGINT");
  await delay(300);
  assert.equal(run.child.exitCode, null, "launcher exited after SIGINT");
  assert.equal(run.child.signalCode, null, "launcher died from SIGINT");
  run.child.stdin.write("finish\n");

  assert.deepEqual(await run.exited, { code: 5, signal: null });
  assert.doesNotMatch(run.stdout, /got SIGINT/);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  test(`dies from ${signal} when the binary was killed by ${signal}`, posixOnly, async (t) => {
    const binary = fakeBinary(tempDir(t), `process.kill(process.pid, ${JSON.stringify(signal)});\nsetInterval(() => {}, 1000);`);
    const run = startLauncher(t, binary);

    assert.deepEqual(await run.exited, { code: null, signal });
  });
}

test("exits 1 with install guidance when the platform package is missing", (t) => {
  const dir = tempDir(t);
  // A copy outside the repository, so no node_modules on the way up can hold the package.
  const isolated = path.join(dir, "cli", "bin", "tello.js");
  fs.mkdirSync(path.dirname(isolated), { recursive: true });
  fs.copyFileSync(launcher, isolated);

  const r = spawnSync(process.execPath, [isolated, "version"], {
    encoding: "utf8",
    env: { ...process.env, TELLO_BINARY_PATH: undefined, NODE_PATH: undefined, HOME: dir, USERPROFILE: dir },
  });

  assert.equal(r.status, 1);
  assert.equal(r.stdout, "");
  assert.match(r.stderr, new RegExp(`@tello-ai/cli-${process.platform}-${process.arch}\\b`));
  assert.match(r.stderr, /--omit=optional/);
  assert.match(r.stderr, process.platform === "win32" ? /install\.ps1/ : /install\.sh/);
});

test("exits 1 on a platform without a prebuilt binary", (t) => {
  const dir = tempDir(t);
  const preload = path.join(dir, "arch.cjs");
  fs.writeFileSync(preload, 'Object.defineProperty(process, "arch", { value: "mips" });\n');

  const r = spawnSync(process.execPath, ["--require", preload, launcher, "version"], {
    encoding: "utf8",
    env: { ...process.env, TELLO_BINARY_PATH: undefined },
  });

  assert.equal(r.status, 1);
  assert.equal(r.stdout, "");
  assert.match(r.stderr, new RegExp(`${process.platform}-mips`));
  assert.match(r.stderr, /TELLO_BINARY_PATH/);
});
