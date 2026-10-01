#!/bin/sh
# Installs the tello CLI from GitHub Releases on macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.sh | sh
#
# Environment:
#   TELLO_VERSION            version to install, e.g. 0.1.0 (default: latest release)
#   TELLO_INSTALL_DIR        directory for the tello binary (default: $HOME/.local/bin)
#   TELLO_DOWNLOAD_BASE_URL  releases URL (default: https://github.com/tello-ai/tello-cli/releases)
#                            Archives are fetched from <base>/download/v<version>/.
set -eu

tmp_dir=
staged=

say() {
	printf '%s\n' "$*"
}

fail() {
	printf 'tello installer: %s\n' "$*" >&2
	exit 1
}

cleanup() {
	if [ -n "$tmp_dir" ]; then rm -rf "$tmp_dir"; fi
	if [ -n "$staged" ]; then rm -f "$staged"; fi
}

# Everything runs from main, called on the last line, so a download cut short by
# `curl | sh` executes nothing.
main() {
	base_url=${TELLO_DOWNLOAD_BASE_URL:-https://github.com/tello-ai/tello-cli/releases}
	base_url=${base_url%/}
	install_dir=${TELLO_INSTALL_DIR:-$HOME/.local/bin}

	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported OS $(uname -s). On Windows, run in PowerShell: irm https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.ps1 | iex" ;;
	esac

	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported architecture $(uname -m); prebuilt binaries exist for amd64 and arm64" ;;
	esac
	# An x86_64 shell under Rosetta 2 still runs on Apple silicon: use the native binary.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
		arch=arm64
	fi

	if command -v curl >/dev/null 2>&1; then
		download() { curl -fsSL --retry 3 -o "$2" "$1"; }
		# Prints the URL that $1 finally redirects to.
		final_url() { curl -fsSL -o /dev/null -w '%{url_effective}' "$1"; }
	elif command -v wget >/dev/null 2>&1; then
		download() { wget -q -O "$2" "$1"; }
		final_url() {
			wget -q -S -O /dev/null "$1" 2>&1 | tr -d '\r' | sed -n 's/^ *[Ll]ocation: *\([^ ]*\).*/\1/p' | tail -n 1
		}
	else
		fail "curl or wget is required"
	fi

	if command -v sha256sum >/dev/null 2>&1; then
		sha256() { sha256sum "$1" | awk '{ print $1 }'; }
	elif command -v shasum >/dev/null 2>&1; then
		sha256() { shasum -a 256 "$1" | awk '{ print $1 }'; }
	else
		fail "sha256sum or shasum is required"
	fi

	version=${TELLO_VERSION:-}
	if [ -z "$version" ]; then
		# <base>/latest redirects to <base>/tag/v<version>.
		latest=$(final_url "$base_url/latest") || latest=
		case "$latest" in
		*/tag/v*) version=${latest##*/tag/v} ;;
		*) fail "could not find the latest release at $base_url/latest; set TELLO_VERSION" ;;
		esac
	fi
	version=${version#v}

	archive="tello_${version}_${os}_${arch}.tar.gz"
	release_url="$base_url/download/v$version"
	tmp_dir=$(mktemp -d 2>/dev/null || mktemp -d -t tello)

	say "Downloading tello $version ($os/$arch)"
	download "$release_url/$archive" "$tmp_dir/$archive" || fail "could not download $release_url/$archive"
	download "$release_url/checksums.txt" "$tmp_dir/checksums.txt" || fail "could not download $release_url/checksums.txt"

	expected=$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1; exit }' "$tmp_dir/checksums.txt")
	[ -n "$expected" ] || fail "checksums.txt has no entry for $archive"
	actual=$(sha256 "$tmp_dir/$archive")
	[ "$actual" = "$expected" ] || fail "checksum mismatch for $archive (expected $expected, got $actual)"

	mkdir "$tmp_dir/extract"
	tar -xzf "$tmp_dir/$archive" -C "$tmp_dir/extract"
	[ -f "$tmp_dir/extract/tello" ] || fail "$archive does not contain tello"

	# Stage next to the target and rename, so a running tello is never overwritten in place.
	mkdir -p "$install_dir"
	staged="$install_dir/.tello.$$"
	cp "$tmp_dir/extract/tello" "$staged"
	chmod 0755 "$staged"
	mv -f "$staged" "$install_dir/tello"
	staged=

	say "Installed $install_dir/tello"
	case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*)
		say ""
		say "$install_dir is not on your PATH. Add it in your shell profile (~/.bashrc, ~/.zshrc, ...):"
		say "  export PATH=\"$install_dir:\$PATH\""
		say ""
		;;
	esac
	"$install_dir/tello" version
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM
main
