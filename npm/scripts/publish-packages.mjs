#!/usr/bin/env node
// Publishes the packages written by build-packages.mjs: every platform package
// first, @tello-ai/cli last, so the launcher never points at binaries that are
// not on the registry yet. Versions already on the registry are skipped, which
// makes a failed release safe to re-run.
//
//   node npm/scripts/publish-packages.mjs [--dry-run] npm/dist
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

function main() {
  const mainName = readJSON(fileURLToPath(new URL("../cli/package.json", import.meta.url))).name;
  const { values, positionals } = parseArgs({
    options: { "dry-run": { type: "boolean", default: false } },
    allowPositionals: true,
  });
  if (positionals.length !== 1) throw new Error("usage: publish-packages.mjs [--dry-run] <dir written by build-packages.mjs>");
  const out = positionals[0];
  // build-packages.mjs writes each package to <out>/<name without the scope>.
  const dirOf = (name) => path.join(out, name.slice(name.indexOf("/") + 1));

  const { version, optionalDependencies = {} } = readJSON(path.join(dirOf(mainName), "package.json"));
  const platforms = Object.keys(optionalDependencies);
  if (platforms.length === 0) throw new Error(`${dirOf(mainName)} has no optionalDependencies; run build-packages.mjs first`);

  for (const name of [...platforms, mainName]) {
    const dir = dirOf(name);
    const manifest = readJSON(path.join(dir, "package.json"));
    if (manifest.name !== name || manifest.version !== version) {
      throw new Error(`${dir} holds ${manifest.name}@${manifest.version}, expected ${name}@${version}`);
    }
    if (isPublished(name, version)) {
      console.log(`skip ${name}@${version}: already published`);
      continue;
    }
    const args = ["publish", "--provenance", "--access", "public"];
    // npm refuses to put a prerelease on `latest` implicitly; ship it as `next`.
    if (version.includes("-")) args.push("--tag", "next");
    if (values["dry-run"]) args.push("--dry-run");
    console.log(`npm ${args.join(" ")}  # ${name}@${version}`);
    const r = spawnSync("npm", args, { cwd: dir, stdio: "inherit" });
    if (r.error) throw r.error;
    if (r.status !== 0) throw new Error(`npm publish failed for ${name}@${version} (exit ${r.status})`);
  }
}

function isPublished(name, version) {
  const r = spawnSync("npm", ["view", `${name}@${version}`, "version"], { encoding: "utf8" });
  if (r.error) throw r.error;
  if (r.status === 0) return r.stdout.trim() !== "";
  // E404: the package or this version does not exist yet.
  if (/\bE404\b/.test(r.stderr)) return false;
  throw new Error(`npm view ${name}@${version} failed:\n${r.stderr.trim()}`);
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

try {
  main();
} catch (err) {
  console.error(`publish-packages: ${err.message}`);
  process.exit(1);
}
