#!/bin/sh
# Installs wirelark from a GitHub release: detects the OS and architecture,
# downloads the matching archive, verifies it against the release's
# checksums.txt, and installs the binary.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/marcelritzschke/wirelark/main/install.sh | sh
#
# Env vars:
#   VERSION      release tag to install, e.g. "v1.2.3" (default: latest)
#   INSTALL_DIR  where to put the binary (default: "$HOME/.local/bin")
#
# This script only reads $HOME and the paths above; it never touches
# ~/.config/wirelark or ~/.cache/wirelark, and it runs `wirelark` itself
# only via --version, to confirm the install worked.

set -eu

repo="marcelritzschke/wirelark"
name="wirelark"

log() {
	printf '%s\n' "$*" >&2
}

die() {
	log "install.sh: $*"
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not found on PATH"
}

# fetch URL to stdout, trying curl then wget.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$1"
	else
		die "need either curl or wget to download $1"
	fi
}

# fetch URL to a file, trying curl then wget.
fetch_to() {
	url=$1
	dest=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$dest" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$dest" "$url"
	else
		die "need either curl or wget to download $url"
	fi
}

detect_os() {
	uname_s=$(uname -s)
	case "$uname_s" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	MINGW* | MSYS* | CYGWIN*)
		die "this script does not support Windows; download the .zip release asset from https://github.com/$repo/releases and add it to your PATH"
		;;
	*) die "unsupported OS: $uname_s" ;;
	esac
}

detect_arch() {
	uname_m=$(uname -m)
	case "$uname_m" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) die "unsupported architecture: $uname_m" ;;
	esac
}

# latest_version prints the tag name of the latest non-prerelease GitHub
# release, e.g. "v1.2.3".
latest_version() {
	fetch "https://api.github.com/repos/$repo/releases/latest" \
		| grep '"tag_name"' \
		| head -n1 \
		| sed -E 's/.*"tag_name": *"([^"]+)".*/\1/'
}

sha256_check() {
	file=$1
	checksums=$2
	if command -v sha256sum >/dev/null 2>&1; then
		( cd "$(dirname "$file")" && grep " $(basename "$file")\$" "$checksums" | sha256sum -c - )
	elif command -v shasum >/dev/null 2>&1; then
		( cd "$(dirname "$file")" && grep " $(basename "$file")\$" "$checksums" | shasum -a 256 -c - )
	else
		die "need either sha256sum or shasum to verify the download"
	fi
}

main() {
	need uname
	need grep
	need sed
	need tar
	need mktemp

	os=$(detect_os)
	arch=$(detect_arch)

	version=${VERSION:-}
	if [ -z "$version" ]; then
		version=$(latest_version)
		[ -n "$version" ] || die "could not determine the latest release; set VERSION=vX.Y.Z and retry"
	fi
	version_num=${version#v}

	install_dir=${INSTALL_DIR:-"$HOME/.local/bin"}

	archive="${name}_${version_num}_${os}_${arch}.tar.gz"
	base_url="https://github.com/$repo/releases/download/$version"

	workdir=$(mktemp -d)
	trap 'rm -rf "$workdir"' EXIT INT TERM

	log "wirelark: downloading $archive ($version)"
	fetch_to "$base_url/$archive" "$workdir/$archive"
	fetch_to "$base_url/checksums.txt" "$workdir/checksums.txt"

	log "wirelark: verifying checksum"
	sha256_check "$workdir/$archive" "$workdir/checksums.txt" \
		|| die "checksum verification failed for $archive"

	tar -xzf "$workdir/$archive" -C "$workdir" "$name"

	mkdir -p "$install_dir"
	dest="$install_dir/$name"
	mv "$workdir/$name" "$dest"
	chmod 755 "$dest"

	log "wirelark: installed to $dest"

	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*)
		log ""
		log "$install_dir is not on your PATH. Add it, e.g.:"
		log "  export PATH=\"$install_dir:\$PATH\""
		;;
	esac

	if [ -x "$dest" ]; then
		"$dest" --version || true
	fi

	log ""
	log "Next: run '$name init' to connect a Feishu app and this machine."
}

main "$@"
