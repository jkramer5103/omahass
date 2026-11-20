#!/usr/bin/env bash
# Render the bar panel to PNGs without touching the real Omarchy shell:
# fake Home Assistant + private omahassd + off-screen Quickshell.
#   tests/preview/run.sh [outdir]   (needs: build/omahassd, a python with aiohttp)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out="$(realpath -m "${1:-$repo/docs/screenshots}")"
py="${PYTHON:-$HOME/.cache/omahass-test/.venv/bin/python}"
work="$(mktemp -d)"
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$work"' EXIT
mkdir -p "$out"

for d in Commons Ui services; do ln -s "/usr/share/omarchy/shell/$d" "$work/$d"; done
ln -s "$repo/plugin/omahass" "$work/omahass"
cp "$here/shell.qml" "$work/shell.qml"

port=$("$py" -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
"$py" "$repo/tests/fake_ha.py" "$port" &
cat > "$work/config.json" <<JSON
{"ha_url":"http://127.0.0.1:$port","ha_token":"demo","agent_port":0,"device_name":"omarchy",
 "favorites":["light.ceiling","light.floor","light.desk","media_player.living_room","climate.hallway","cover.blinds","scene.movie","sensor.plant_temp","sensor.weather_temp"],
 "custom_commands":[{"id":"a1b2c3","name":"Open browser","command":"omarchy-launch-browser"}]}
JSON
# agent_port 0 -> defaults; use a free one so a real install isn't disturbed
agent=$("$py" -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
sed -i "s/\"agent_port\":0/\"agent_port\":$agent/" "$work/config.json"
export OMAHASS_SOCKET="$work/ui.sock" OMAHASS_DISPLAY_IP=192.168.1.50
"$repo/build/omahassd" -config "$work/config.json" -no-mdns >"$work/daemon.log" 2>&1 &
export PATH="$repo/build:$PATH" PREVIEW_OUT="$out"
QT_QPA_PLATFORM="${QT_QPA_PLATFORM:-offscreen}" qs -p "$work/shell.qml" 2>&1 | grep -v '^\s*$' | grep -iv 'debug' | tail -25 || true
ls -la "$out"
