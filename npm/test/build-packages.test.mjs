// Tests for npm/scripts/build-packages.mjs against a fake GoReleaser dist.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const buildScript = fileURLToPath(new URL("../scripts/build-packages.mjs", import.meta.url));

// [goos, goarch, npm os, npm cpu]
const PLATFORMS = [
  ["darwin", "amd64", "darwin", "x64"],
  ["darwin", "arm64", "darwin", "arm64"],
  ["linux", "amd64", "linux", "x64"],
  ["linux", "arm64", "linux", "arm64"],
  ["windows", "amd64", "win32", "x64"],
  ["windows", "arm64", "win32", "arm64"],
];

function tempDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tello-build-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// Lays out <root>/dist like GoReleaser: artifacts.json paths are relative to the
// project root. Every fake binary is a shell script naming its platform.
function fakeDist(root, platforms) {
  const artifacts = [];
  for (const [goos, goarch] of platforms) {
    const exe = goos === "windows" ? "tello.exe" : "tello";
    const rel = `dist/tello_${goos}_${goarch}_${goarch === "amd64" ? "v1" : "v8.0"}/${exe}`;
    fs.mkdirSync(path.dirname(path.join(root, rel)), { recursive: true });
    fs.writeFileSync(path.join(root, rel), `#!/bin/sh\necho "tello ${goos}/${goarch} $*"\nexit 3\n`, { mode: 0o644 });
    artifacts.push({ name: exe, path: rel, goos, goarch, internal_type: 4, type: "Binary" });
    const archive = `tello_1.2.3_${goos}_${goarch}.${goos === "windows" ? "zip" : "tar.gz"}`;
    artifacts.push({ name: archive, path: `dist/${archive}`, goos, goarch, internal_type: 1, type: "Archive" });
  }
  artifacts.push({ name: "checksums.txt", path: "dist/checksums.txt", internal_type: 12, type: "Checksum" });
  fs.mkdirSync(path.join(root, "dist"), { recursive: true });
  fs.writeFileSync(path.join(root, "dist", "artifacts.json"), JSON.stringify(artifacts));
  return path.join(root, "dist");
}

function build(dist, out, version = "1.2.3") {
  return spawnSync(process.execPath, [buildScript, "--version", version, "--dist", dist, "--out", out], {
    encoding: "utf8",
  });
}

// name -> { dir, manifest } for every package directory under out.
function readPackages(out) {
  const packages = new Map();
  for (const entry of fs.readdirSync(out)) {
    const manifestPath = path.join(out, entry, "package.json");
    if (!fs.existsSync(manifestPath)) continue;
    const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
    packages.set(manifest.name, { dir: path.join(out, entry), manifest });
  }
  return packages;
}

const platformName = (npmOs, cpu) => `@tello-ai/cli-${npmOs}-${cpu}`;

test("builds one package per platform and a main package pinned to all of them", (t) => {
  const root = tempDir(t);
  const out = path.join(root, "out");

  const r = build(fakeDist(root, PLATFORMS), out);
  assert.equal(r.status, 0, r.stderr);

  const packages = readPackages(out);
  const platformNames = PLATFORMS.map(([, , npmOs, cpu]) => platformName(npmOs, cpu));
  assert.deepEqual([...packages.keys()].sort(), ["@tello-ai/cli", ...platformNames].sort());

  for (const [goos, goarch, npmOs, cpu] of PLATFORMS) {
    const { dir, manifest } = packages.get(platformName(npmOs, cpu));
    assert.equal(manifest.version, "1.2.3");
    assert.deepEqual(manifest.os, [npmOs]);
    assert.deepEqual(manifest.cpu, [cpu]);
    assert.equal(manifest.bin, undefined);
    assert.equal(manifest.scripts, undefined);
    assert.equal(manifest.license, "Apache-2.0");
    const binary = path.join(dir, "bin", npmOs === "win32" ? "tello.exe" : "tello");
    assert.match(fs.readFileSync(binary, "utf8"), new RegExp(`tello ${goos}/${goarch} `));
    if (process.platform !== "win32") assert.equal(fs.statSync(binary).mode & 0o777, 0o755);
  }

  const main = packages.get("@tello-ai/cli").manifest;
  assert.equal(main.version, "1.2.3");
  assert.deepEqual(main.bin, { tello: "bin/tello.js" });
  assert.deepEqual(main.optionalDependencies, Object.fromEntries(platformNames.map((name) => [name, "1.2.3"])));
});

test("installed packages run this platform's binary through the launcher", { skip: process.platform === "win32" }, (t) => {
  const root = tempDir(t);
  const out = path.join(root, "out");
  assert.equal(build(fakeDist(root, PLATFORMS), out).status, 0);

  // node_modules as npm lays it out: the launcher next to this platform's package.
  const packages = readPackages(out);
  const scope = path.join(root, "app", "node_modules", "@tello-ai");
  const own = platformName(process.platform, process.arch);
  fs.cpSync(packages.get("@tello-ai/cli").dir, path.join(scope, "cli"), { recursive: true });
  fs.cpSync(packages.get(own).dir, path.join(scope, own.slice("@tello-ai/".length)), { recursive: true });

  const r = spawnSync(process.execPath, [path.join(scope, "cli", "bin", "tello.js"), "version", "--json"], {
    encoding: "utf8",
    env: { ...process.env, TELLO_BINARY_PATH: undefined },
  });

  const goos = { darwin: "darwin", linux: "linux" }[process.platform];
  const goarch = { x64: "amd64", arm64: "arm64" }[process.arch];
  assert.equal(r.stderr, "");
  assert.equal(r.stdout, `tello ${goos}/${goarch} version --json\n`);
  assert.equal(r.status, 3);
});

test("fails without writing packages when a platform binary is missing", async (t) => {
  const cases = {
    "not in artifacts.json": (root) =>
      fakeDist(root, PLATFORMS.filter(([goos, goarch]) => !(goos === "windows" && goarch === "arm64"))),
    "listed but not on disk": (root) => {
      const dist = fakeDist(root, PLATFORMS);
      fs.rmSync(path.join(dist, "tello_windows_arm64_v8.0", "tello.exe"));
      return dist;
    },
  };
  for (const [name, makeDist] of Object.entries(cases)) {
    await t.test(name, (t) => {
      const root = tempDir(t);
      const out = path.join(root, "out");

      const r = build(makeDist(root), out);

      assert.equal(r.status, 1);
      assert.match(r.stderr, /windows\/arm64/);
      assert.deepEqual(fs.existsSync(out) ? fs.readdirSync(out) : [], []);
    });
  }
});
