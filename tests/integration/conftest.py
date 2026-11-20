"""Run the integration inside Home Assistant's test harness against a real
omahassd process (mDNS off, temp config, private socket and port)."""

from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import time
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))  # so custom_components.omahass is importable

BINARY = ROOT / "build" / "omahassd"

# Home Assistant's harness blocks unix sockets per test; the panel socket is
# local test plumbing, so keep a handle on the real connect.
_REAL_CONNECT = socket.socket.connect


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations):
    yield


@pytest.fixture
def daemon(tmp_path, socket_enabled):
    port = _free_port()
    cfg = tmp_path / "config.json"
    cfg.write_text(json.dumps({"agent_port": port, "device_name": "Test PC", "agent_key": "abcd1234"}))
    sock = tmp_path / "ui.sock"
    env = {**os.environ, "OMAHASS_SOCKET": str(sock)}
    proc = subprocess.Popen(
        [str(BINARY), "-config", str(cfg), "-no-mdns"],
        env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
    )
    deadline = time.time() + 10
    while time.time() < deadline:
        try:
            socket.create_connection(("127.0.0.1", port), timeout=0.2).close()
            if sock.exists():
                break
        except OSError:
            time.sleep(0.1)
    else:
        proc.kill()
        raise RuntimeError("omahassd did not start: " + proc.stdout.read().decode())

    class Daemon:
        host = "127.0.0.1"
        key = "abcd1234"
        tmp = tmp_path

        def __init__(self):
            self.port = port

        def ui(self, op: dict) -> list[dict]:
            """Send one op over the panel socket, return what came back."""
            with socket.socket(socket.AF_UNIX) as s:
                _REAL_CONNECT(s, str(sock))
                s.sendall((json.dumps(op) + "\n").encode())
                s.settimeout(0.5)
                buf = b""
                try:
                    while chunk := s.recv(65536):
                        buf += chunk
                except TimeoutError:
                    pass
            return [json.loads(l) for l in buf.decode().splitlines() if l]

        def device_id(self) -> str:
            for m in self.ui({"op": "refresh"}):
                if m.get("ev") == "status":
                    return m["agent"]["device_id"]
            raise AssertionError("no status event")

    yield Daemon()
    proc.terminate()
    proc.wait(5)
