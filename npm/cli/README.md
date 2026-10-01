# @tello-ai/cli

`tello` places Tello phone calls, answers each turn live, and fetches call summaries from a terminal or a coding agent (Claude Code, Codex).

## Install

```sh
npm install -g @tello-ai/cli
```

Requires Node.js 18 or later. npm installs a prebuilt binary for your platform (macOS, Linux, Windows on x64 or arm64) as an optional dependency, so do not install with `--omit=optional`.

Without Node.js:

```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.sh | sh
```

```powershell
# Windows PowerShell
irm https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.ps1 | iex
```

## Usage

```sh
tello auth login                       # store your API key
tello call create --to +821012345678 -i   # place a call and answer from the terminal
tello call summary <callId>            # transcript and summary of a finished call
tello init                             # add the Tello skill for Claude Code and Codex
tello guide coding-agent               # operating guide for coding agents
```

Every command accepts `--json` for one JSON object per line on stdout, and exit codes tell outcomes apart. Run `tello --help` for all commands.

In CI, set `TELLO_API_KEY` instead of running `tello auth login`.

## License

Apache-2.0. Source: https://github.com/tello-ai/tello-cli
