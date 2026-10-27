# @tello-ai/cli

`tello` places and follows Tello phone calls with a portal agent or crew, or lets you answer each turn live from a terminal or coding agent (Claude Code, Codex).

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
tello auth login                          # store your API key
tello agent list                          # list deployed agents
tello call create --to +821012345678 --agent 12 --wait
tello call get <callId>                   # status, transcript and summary
tello call create --to +821012345678 -i   # answer turns yourself
tello init                                # add the Tello skill for Claude Code and Codex
tello guide coding-agent                  # operating guide for coding agents
```

Every command accepts `--json` for one JSON object per line on stdout, and exit codes tell outcomes apart. Run `tello --help` for all commands.

In CI, set `TELLO_API_KEY` instead of running `tello auth login`.

## License

Apache-2.0. Source: https://github.com/tello-ai/tello-cli
