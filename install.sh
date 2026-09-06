#!/bin/sh
# Install a checksum-verified Elephant GitHub release. No sudo or shell edits.
set -eu

fail() { printf 'elephant installer: %s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'USAGE'
Usage: sh install.sh [--repo OWNER/REPO] [--version VERSION] [--dir DIRECTORY]

Defaults: 0ctacity/elephant; latest stable release; $HOME/.local/bin.
Use --version 1.0.0-rc.1 to select a prerelease explicitly.
ELEPHANT_REPO and ELEPHANT_INSTALL_DIR can also set the repository and directory.
Windows users need Git Bash (with curl and unzip) or can extract the release ZIP.
USAGE
}

main() {
    repo=${ELEPHANT_REPO:-0ctacity/elephant}
    version=latest
    install_dir=${ELEPHANT_INSTALL_DIR:-${HOME:?HOME is not set}/.local/bin}
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --repo|--version|--dir)
                [ "$#" -ge 2 ] || fail "$1 requires a value"
                case "$1" in
                    --repo) repo=$2 ;;
                    --version) version=$2 ;;
                    --dir) install_dir=$2 ;;
                esac
                shift 2 ;;
            -h|--help) usage; return ;;
            *) fail "unknown option: $1" ;;
        esac
    done
    printf '%s\n' "$repo" | LC_ALL=C grep -Eq '^[A-Za-z0-9_-]+/[A-Za-z0-9_.-]+$' ||
        fail 'set --repo OWNER/REPO or ELEPHANT_REPO to the GitHub release repository'
    [ -n "$install_dir" ] || fail 'installation directory must not be empty'
    case "$install_dir" in /*) ;; *) install_dir=$PWD/$install_dir ;; esac
    for tool in curl uname mktemp awk; do
        command -v "$tool" >/dev/null 2>&1 || fail "required command not found: $tool"
    done
    system=$(uname -s)
    arch=$(uname -m)
    case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
    executable=elephant
    extension=tar.gz
    case "$system:$arch" in
        Linux:amd64|Linux:arm64) target=linux-$arch ;;
        Darwin:arm64) target=macos-arm64 ;;
        MINGW*:amd64|MSYS*:amd64) target=windows-amd64; executable=elephant.exe; extension=zip ;;
        *) fail "unsupported platform: $system $arch" ;;
    esac
    if [ "$extension" = zip ]; then
        command -v unzip >/dev/null 2>&1 || fail 'unzip is required'
    else
        command -v tar >/dev/null 2>&1 || fail 'tar is required'
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        checksum_tool=sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        checksum_tool=shasum
    else
        fail 'sha256sum or shasum is required'
    fi
    if [ "$version" = latest ]; then
        printf 'Resolving latest stable Elephant release...\n' >&2
        release_url=$(curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 120 -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
            fail 'could not resolve a stable release; use --version for a prerelease'
        case "$release_url" in
            "https://github.com/$repo/releases/tag/"*) version=${release_url##*/} ;;
            *) fail 'no stable release found; use --version for a prerelease' ;;
        esac
    fi
    case "$version" in v*) ;; *) version=v$version ;; esac
    printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*)?$' ||
        fail 'invalid version; expected 1.0.0 or 1.0.0-rc.2'

    archive=elephant-$version-$target.$extension
    base=https://github.com/$repo/releases/download/$version
    work=$(mktemp -d "${TMPDIR:-/tmp}/elephant-install.XXXXXXXX") || fail 'cannot create temporary directory'
    staged=
    trap 'rm -rf "$work"; if [ -n "$staged" ]; then rm -f "$staged"; fi' 0
    trap 'exit 1' HUP INT TERM
    printf 'Downloading Elephant %s for %s...\n' "$version" "$target" >&2
    curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 300 -o "$work/$archive" "$base/$archive" || fail 'release archive download failed'
    curl --proto '=https' --proto-redir '=https' -fsSL --connect-timeout 15 --max-time 120 -o "$work/SHA256SUMS.txt" "$base/SHA256SUMS.txt" || fail 'checksum download failed'
    expected=$(awk -v name="$archive" '$2 == name {count++; hash=$1} END {if(count!=1) exit 1; print hash}' "$work/SHA256SUMS.txt") || fail 'missing or duplicate archive checksum'
    printf '%s\n' "$expected" | LC_ALL=C grep -Eq '^[0-9a-f]{64}$' || fail 'invalid checksum manifest'
    if [ "$checksum_tool" = sha256sum ]; then
        actual=$(sha256sum "$work/$archive")
    else
        actual=$(shasum -a 256 "$work/$archive")
    fi
    actual=${actual%% *}
    [ "$actual" = "$expected" ] || fail 'checksum mismatch; existing installation has not been changed'

    # Extract only the executable to stdout: archive paths cannot write files.
    if [ "$extension" = zip ]; then
        unzip -p "$work/$archive" "$executable" > "$work/binary" || fail 'cannot extract executable'
    else
        tar -xOzf "$work/$archive" "$executable" > "$work/binary" || fail 'cannot extract executable'
    fi
    [ -s "$work/binary" ] || fail 'archive contains no executable'
    mkdir -p "$install_dir" || fail 'cannot create installation directory'
    [ ! -d "$install_dir/$executable" ] || fail 'destination is a directory'
    staged=$(mktemp "$install_dir/.elephant-install.XXXXXXXX") || fail 'installation directory is not writable'
    cat "$work/binary" > "$staged"
    chmod 755 "$staged"
    mv -f "$staged" "$install_dir/$executable" || fail 'cannot replace executable'
    staged=
    printf 'Installed %s to %s/%s\n' "$version" "$install_dir" "$executable"
    case ":${PATH:-}:" in
        *":$install_dir:"*) ;;
        *) printf 'Add %s to your PATH to run elephant from any directory.\n' "$install_dir" ;;
    esac
}

# Run only after the complete script has been read when piped into sh.
main "$@"
