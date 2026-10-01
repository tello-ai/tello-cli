#!/usr/bin/env node
// Builds the npm packages for a release from GoReleaser's dist directory:
// one @tello-ai/cli-<os>-<cpu> package per platform holding just the binary,
// and @tello-ai/cli (the launcher in npm/cli) with optionalDependencies pinned
// to them.
//
//   node npm/scripts/build-packages.mjs --version 1.2.3 --dist dist --out npm/dist
//
// Each package is written to <out>/<package name without the scope>.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const templateDir = fileURLToPath(new URL("../cli", import.meta.url));
const licenseFile = fileURLToPath(new URL("../../LICENSE", import.meta.url));

// GoReleaser goos/goarch -> npm os/cpu.
const NPM_OS = { darwin: "darwin", linux: "linux", windows: "win32" };
const NPM_CPU = { amd64: "x64", arm64: "arm64" };

function main() {
  const { values } = parseArgs({
    options: { version: { type: "string" }, dist: { type: "string" }, out: { type: "string" } },
  });
  const { version, dist, out } = values;
  if (!version || !dist || !out) {
    throw new Error("usage: build-packages.mjs --version <x.y.z> --dist <goreleaser dist dir> --out <dir>");
  }

  const template = readJSON(path.join(templateDir, "package.json"));
  const binaries = findBinaries(dist);

  const optionalDependencies = {};
  for (const { os, cpu, file } of binaries) {
    const name = `${template.name}-${os}-${cpu}`;
    const dir = freshPackageDir(out, name);
    const exe = path.join(dir, "bin", os === "win32" ? "tello.exe" : "tello");
    fs.mkdirSync(path.dirname(exe));
    fs.copyFileSync(file, exe);
    fs.chmodSync(exe, 0o755);
    fs.copyFileSync(licenseFile, path.join(dir, "LICENSE"));
    writeJSON(path.join(dir, "package.json"), {
      name,
      version,
      description: `The ${os}-${cpu} binary for ${template.name}.`,
      homepage: template.homepage,
      license: template.license,
      repository: template.repository,
      os: [os],
      cpu: [cpu],
    });
    optionalDependencies[name] = version;
  }

  const dir = freshPackageDir(out, template.name);
  fs.cpSync(templateDir, dir, { recursive: true });
  fs.copyFileSync(licenseFile, path.join(dir, "LICENSE"));
  writeJSON(path.join(dir, "package.json"), { ...template, version, optionalDependencies });

  console.log(`built ${template.name}@${version} and ${binaries.length} platform packages in ${out}`);
}

// Finds the binary of every platform in <dist>/artifacts.json before anything is
// written, so a partial build never produces publishable packages.
function findBinaries(dist) {
  const artifactsFile = path.join(dist, "artifacts.json");
  const artifacts = readJSON(artifactsFile);
  // Artifact paths are relative to the directory GoReleaser ran in, which holds dist.
  const root = path.dirname(path.resolve(dist));

  const binaries = [];
  const missing = [];
  for (const [goos, os] of Object.entries(NPM_OS)) {
    for (const [goarch, cpu] of Object.entries(NPM_CPU)) {
      const artifact = artifacts.find((a) => a.type === "Binary" && a.goos === goos && a.goarch === goarch);
      const file = artifact && path.resolve(root, artifact.path);
      if (file && fs.existsSync(file)) binaries.push({ os, cpu, file });
      else missing.push(`${goos}/${goarch}`);
    }
  }
  if (missing.length > 0) throw new Error(`no binary for ${missing.join(", ")} in ${artifactsFile}`);
  return binaries;
}

function freshPackageDir(out, name) {
  const dir = path.join(out, name.slice(name.indexOf("/") + 1));
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  return dir;
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function writeJSON(file, value) {
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

try {
  main();
} catch (err) {
  console.error(`build-packages: ${err.message}`);
  process.exit(1);
}
