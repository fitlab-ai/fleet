#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
target_dir=${FLEET_INSTALL_DIR:-"$HOME/.local/bin"}
target="$target_dir/fleet"
build_dir=$(mktemp -d "${TMPDIR:-/tmp}/fleet-install.XXXXXX")
trap 'rm -rf "$build_dir"' EXIT HUP INT TERM

mkdir -p "$target_dir"
cd "$repo_root"
go test ./...
go build -trimpath -o "$build_dir/fleet" ./cmd/fleet
"$build_dir/fleet" help >/dev/null

backup=
if [ -e "$target" ]; then
  backup="$target.backup.$(date +%Y%m%d%H%M%S)"
  mv "$target" "$backup"
fi
install -m 0755 "$build_dir/fleet" "$target.new"
mv "$target.new" "$target"

printf 'Installed Fleet Go binary: %s\n' "$target"
if [ -n "$backup" ]; then
  printf 'Rollback: mv %s %s\n' "$backup" "$target"
fi
