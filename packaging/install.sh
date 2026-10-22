#!/bin/sh
# Installs Sermon Studio for this user: the program in ~/.local/bin, and an entry in the applications menu.
set -eu
cd "$(dirname "$0")"
bin=$HOME/.local/bin
apps=${XDG_DATA_HOME:-$HOME/.local/share}/applications
icons=${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/512x512/apps
mkdir -p "$bin" "$apps" "$icons"
cp sermon-studio-desktop "$bin/" && chmod 755 "$bin/sermon-studio-desktop"
cp sermon-studio.png "$icons/"
sed "s|^Exec=.*|Exec=\"$bin/sermon-studio-desktop\"|" sermon-studio.desktop > "$apps/sermon-studio.desktop"
command -v update-desktop-database >/dev/null && update-desktop-database "$apps" 2>/dev/null || true
echo "Sermon Studio is installed: find it in the applications menu, or run $bin/sermon-studio-desktop"
