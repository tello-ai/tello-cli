#!/usr/bin/env node
// Runs the native tello binary from the @tello-ai/cli-<platform>-<arch> package
// that npm installed as an optional dependency.
"use strict";

const { spawn } = require("node:child_process");
const os = require("node:os");

const PLATFORMS = ["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64", "win32-arm64", "win32-x64"];

const INSTALLER =
  process.platform === "win32"
    ? "irm https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.ps1 | iex"
    : "curl -fsSL https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.sh | sh";

function fail(message) {
  process.stderr.write(`tello: ${message}\n`);
  process.exit(1);
}

function binaryPath() {
  if (process.env.TELLO_BINARY_PATH) return process.env.TELLO_BINARY_PATH;

  const platform = `${process.platform}-${process.arch}`;
  if (!PLATFORMS.includes(platform)) {
    fail(
      `no prebuilt binary for ${platform} (available: ${PLATFORMS.join(", ")}).\n` +
        "Build tello from source and point TELLO_BINARY_PATH at it.",
    );
  }
  const pkg = `@tello-ai/cli-${platform}`;
  try {
    return require.resolve(`${pkg}/bin/${process.platform === "win32" ? "tello.exe" : "tello"}`);
  } catch {
    fail(
      `the platform package ${pkg} is not installed.\n` +
        "npm skips it when optional dependencies are omitted (--omit=optional). Reinstall with them:\n" +
        "  npm install -g @tello-ai/cli\n" +
        "or install the standalone binary:\n" +
        `  ${INSTALLER}`,
    );
  }
}

const bin = binaryPath();
let child;
try {
  child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });
} catch (err) {
  fail(`cannot run ${bin}: ${err.message}`);
}
child.on("error", (err) => fail(`cannot run ${bin}: ${err.message}`));

// Ctrl-C reaches tello directly through the terminal's process group.
// Forwarding SIGINT would deliver it twice, and tello treats a second Ctrl-C as
// "exit now" instead of "cancel the call".
process.on("SIGINT", () => {});
for (const signal of ["SIGTERM", "SIGHUP"]) {
  process.on(signal, () => {
    try {
      child.kill(signal);
    } catch {
      // The signal does not exist on this platform (Windows); tello gets the
      // console event itself.
    }
  });
}

child.on("exit", (code, signal) => {
  if (!signal) process.exit(code);
  // End the same way tello did so callers see the same status.
  for (const handled of ["SIGINT", "SIGTERM", "SIGHUP"]) process.removeAllListeners(handled);
  process.kill(process.pid, signal);
  // Still running: Node ignores this signal (SIGPIPE). Use the shell convention.
  process.exit(128 + (os.constants.signals[signal] || 0));
});
