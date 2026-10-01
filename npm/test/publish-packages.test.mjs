// Tests for npm/scripts/publish-packages.mjs with a fake `npm` first on PATH.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const buildScript = fileURLToPath(new URL("../scripts/build-packages.mjs", import.meta.url));
const publishScript = fileURLToPath(new URL("../scripts/publish-packages.mjs", import.meta.url));
const posixOnly = { skip: process.platform === "win32" && "fake npm is a shebang script" };

const PLATFORM_NAMES = [
  "@tello-ai/cli-darwin-arm64",
  "@tello-ai/cli-darwin-x64",
  "@tello-ai/cli-linux-arm64",
  "@tello-ai/cli-linux-x64",
  "@tello-ai/cli-win32-arm64",
  "@tello-ai/cli-win32-x64",
];

// The fake npm logs every call as a JSON line. `npm view` reports the specs in
// FAKE_NPM_PUBLISHED as published and everything else as E404; `npm publish`
// fails for the package named in FAKE_NPM_FAIL.
const FAKE_NPM = `
const fs = require("node:fs");
const [command, ...args] = process.argv.slice(2);
const log = (entry) => fs.appendFileSync(process.env.FAKE_NPM_LOG, JSON.stringify(entry) + "\\n");
if (command === "view") {
  const spec = args[0];
  log({ command, spec, args: args.slice(1) });
  if ((process.env.FAKE_NPM_PUBLISHED || "").split(" ").includes(spec)) {
    console.log(spec.slice(spec.lastIndexOf("@") + 1));
    process.exit(0);
  }
  console.error("npm error code E404");
  process.exit(1);
}
if (command === "publish") {
  // Flags with a value: --access <level>, --tag <dist-tag>. Any other bare
  // argument is the package directory.
  const flags = [];
  let dir = process.cwd();
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--access" || args[i] === "--tag") flags.push(args[i], args[++i]);
    else if (args[i].startsWith("-")) flags.push(args[i]);
    else dir = args[i];
  }
  const { name, version } = JSON.parse(fs.readFileSync(dir + "/package.json", "utf8"));
  log({ command, name, version, args: flags });
  process.exit(name === process.env.FAKE_NPM_FAIL ? 1 : 0);
}
process.exit(2);
`;

function tempDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tello-publish-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// Builds real packages from a fake GoReleaser dist under root.
function buildPackages(root, version = "1.2.3") {
  const artifacts = [];
  for (const goos of ["darwin", "linux", "windows"]) {
    for (const goarch of ["amd64", "arm64"]) {
      const rel = `dist/tello_${goos}_${goarch}/tello${goos === "windows" ? ".exe" : ""}`;
      fs.mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
      fs.writeFileSync(path.join(root, rel), "binary");
      artifacts.push({ name: path.basename(rel), path: rel, goos, goarch, type: "Binary" });
    }
  }
  fs.writeFileSync(path.join(root, "dist", "artifacts.json"), JSON.stringify(artifacts));
  const out = path.join(root, "out");
  const r = spawnSync(process.execPath, [buildScript, "--version", version, "--dist", path.join(root, "dist"), "--out", out], {
    encoding: "utf8",
  });
  assert.equal(r.status, 0, r.stderr);
  return out;
}

function publish(t, { args = [], published = [], fail = "", version } = {}) {
  const root = tempDir(t);
  const out = buildPackages(root, version);
  const bin = path.join(root, "bin");
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, "npm"), `#!${process.execPath}\n${FAKE_NPM}`, { mode: 0o755 });
  const log = path.join(root, "npm.log");
  fs.writeFileSync(log, "");

  const r = spawnSync(process.execPath, [publishScript, ...args, out], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${bin}${path.delimiter}${process.env.PATH}`,
      FAKE_NPM_LOG: log,
      FAKE_NPM_PUBLISHED: published.join(" "),
      FAKE_NPM_FAIL: fail,
    },
  });
  const calls = fs.readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
  return { ...r, publishes: calls.filter((call) => call.command === "publish") };
}

test("publishes every platform package before the main package", posixOnly, (t) => {
  const r = publish(t);

  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(
    r.publishes.map((call) => call.version),
    Array(7).fill("1.2.3"),
  );
  assert.deepEqual(r.publishes.slice(0, 6).map((call) => call.name).sort(), PLATFORM_NAMES);
  assert.equal(r.publishes[6].name, "@tello-ai/cli");
  for (const call of r.publishes) {
    assert.ok(call.args.includes("--provenance"), JSON.stringify(call));
    assert.ok(call.args.includes("--access") && call.args.includes("public"), JSON.stringify(call));
    assert.ok(!call.args.includes("--dry-run"), JSON.stringify(call));
  }
});

test("publishes prereleases under the next dist-tag, releases under latest", posixOnly, (t) => {
  const pre = publish(t, { version: "1.3.0-rc.1" });
  assert.equal(pre.status, 0, pre.stderr);
  assert.equal(pre.publishes.length, 7);
  for (const call of pre.publishes) {
    assert.deepEqual(call.args.slice(call.args.indexOf("--tag"), call.args.indexOf("--tag") + 2), ["--tag", "next"], JSON.stringify(call));
  }

  const release = publish(t);
  for (const call of release.publishes) assert.ok(!call.args.includes("--tag"), JSON.stringify(call));
});

test("skips versions that are already published", posixOnly, (t) => {
  const r = publish(t, { published: ["@tello-ai/cli-linux-x64@1.2.3", "@tello-ai/cli-darwin-arm64@1.2.3"] });

  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(
    r.publishes.map((call) => call.name).sort(),
    [...PLATFORM_NAMES.filter((name) => !/linux-x64|darwin-arm64/.test(name)), "@tello-ai/cli"].sort(),
  );
  assert.equal(r.publishes.at(-1).name, "@tello-ai/cli");
});

test("passes --dry-run to npm publish", posixOnly, (t) => {
  const r = publish(t, { args: ["--dry-run"] });

  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.publishes.length, 7);
  for (const call of r.publishes) assert.ok(call.args.includes("--dry-run"), JSON.stringify(call));
});

test("stops before the main package when a platform package fails to publish", posixOnly, (t) => {
  const r = publish(t, { fail: "@tello-ai/cli-win32-arm64" });

  assert.equal(r.status, 1);
  assert.match(r.stderr, /@tello-ai\/cli-win32-arm64/);
  assert.ok(!r.publishes.some((call) => call.name === "@tello-ai/cli"));
});
