"""WebSocket client for the omahassd agent.

Deliberately free of Home Assistant imports (only aiohttp) so it can be
exercised against a real daemon without a Home Assistant install.

Protocol (JSON text frames):
    -> {"type": "hello", "key": "<pairing key>"}
    <- {"type": "welcome", "device": {...}, "entities": [...], "states": {...}}
       or {"type": "error", "error": "invalid_key"}
    <- {"type": "states", "states": {key: value}}           changed values
    <- {"type": "entities", "device", "entities", "states"}  schema changed
    <- {"type": "event", "event": "...", "data": {...}}
    -> {"type": "command", "id": n, "key": "...", "value": ...}
    <- {"type": "result", "id": n, "ok": bool, "error": "..."}
"""

from __future__ import annotations

import asyncio
import contextlib
import itertools
import logging
from collections.abc import Callable
from typing import Any

import aiohttp

_LOGGER = logging.getLogger(__name__)


class OmahassError(Exception):
    """Base error."""


class CannotConnect(OmahassError):
    """The agent could not be reached."""


class InvalidKey(OmahassError):
    """The agent rejected the pairing key."""


class CommandFailed(OmahassError):
    """The PC could not run a command."""


class OmahassClient:
    """Keeps one live connection to a PC and mirrors its entities/states."""

    def __init__(
        self, session: aiohttp.ClientSession, host: str, port: int, key: str
    ) -> None:
        self._session = session
        self.host = host
        self.port = port
        self.key = key
        self.device: dict[str, Any] = {}
        self.entities: list[dict[str, Any]] = []
        self.states: dict[str, Any] = {}
        self.connected = False

        self._ws: aiohttp.ClientWebSocketResponse | None = None
        self._ids = itertools.count(1)
        self._pending: dict[int, asyncio.Future[None]] = {}
        self._listeners: set[Callable[[set[str] | None], None]] = set()
        self._task: asyncio.Task[None] | None = None
        self._first: asyncio.Future[None] | None = None

        self.on_schema: Callable[[list[dict[str, Any]], dict[str, Any]], None] | None = None
        self.on_event: Callable[[str, dict[str, Any]], None] | None = None

    @property
    def url(self) -> str:
        host = f"[{self.host}]" if ":" in self.host else self.host
        return f"http://{host}:{self.port}"

    def art_url(self) -> str:
        return f"{self.url}/art?key={self.key}"

    # -- listeners ---------------------------------------------------------

    def add_listener(self, cb: Callable[[set[str] | None], None]) -> Callable[[], None]:
        """Call cb(changed_keys) on state pushes, cb(None) on (dis)connect."""
        self._listeners.add(cb)
        return lambda: self._listeners.discard(cb)

    def _notify(self, keys: set[str] | None) -> None:
        for cb in list(self._listeners):
            try:
                cb(keys)
            except Exception:  # noqa: BLE001 - one bad entity must not stop the rest
                _LOGGER.exception("listener failed")

    # -- connection --------------------------------------------------------

    async def _open(self) -> aiohttp.ClientWebSocketResponse:
        try:
            ws = await self._session.ws_connect(
                f"{self.url}/ws",
                heartbeat=30,
                timeout=aiohttp.ClientWSTimeout(ws_close=10),
                max_msg_size=8 * 1024 * 1024,
            )
        except (aiohttp.ClientError, OSError, asyncio.TimeoutError) as err:
            raise CannotConnect(str(err)) from err
        try:
            await ws.send_json({"type": "hello", "key": self.key})
            msg = await ws.receive(timeout=10)
        except (aiohttp.ClientError, asyncio.TimeoutError) as err:
            await ws.close()
            raise CannotConnect(str(err)) from err
        if msg.type != aiohttp.WSMsgType.TEXT:
            await ws.close()
            raise InvalidKey("connection closed during handshake")
        data = msg.json()
        if data.get("type") == "error":
            await ws.close()
            raise InvalidKey(data.get("error", "rejected"))
        if data.get("type") != "welcome":
            await ws.close()
            raise CannotConnect(f"unexpected handshake {data.get('type')}")
        self._apply_snapshot(data)
        return ws

    async def probe(self) -> dict[str, Any]:
        """Connect once, return device info, disconnect. For the config flow."""
        ws = await self._open()
        await ws.close()
        return self.device

    def _apply_snapshot(self, data: dict[str, Any]) -> None:
        self.device = data.get("device") or {}
        self.entities = data.get("entities") or []
        self.states = dict(data.get("states") or {})

    async def start(self, timeout: float = 15) -> None:
        """Connect and keep reconnecting. Returns after the first welcome."""
        loop = asyncio.get_running_loop()
        self._first = loop.create_future()
        self._task = loop.create_task(self._run())
        try:
            await asyncio.wait_for(asyncio.shield(self._first), timeout)
        except BaseException:
            await self.stop()
            raise

    async def stop(self) -> None:
        if self._task:
            self._task.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await self._task
            self._task = None
        if self._ws and not self._ws.closed:
            await self._ws.close()
        self._set_connected(False)

    def _set_connected(self, value: bool) -> None:
        if self.connected != value:
            self.connected = value
            self._notify(None)

    async def _run(self) -> None:
        backoff = 1.0
        first_schema = True
        while True:
            try:
                self._ws = await self._open()
            except InvalidKey as err:
                if self._first and not self._first.done():
                    self._first.set_exception(err)
                    return
                _LOGGER.warning("PC %s rejected the pairing key", self.host)
                await asyncio.sleep(60)
                continue
            except CannotConnect as err:
                if self._first and not self._first.done():
                    self._first.set_exception(err)
                    return
                _LOGGER.debug("PC %s unreachable: %s", self.host, err)
                await asyncio.sleep(backoff)
                backoff = min(backoff * 2, 60)
                continue

            backoff = 1.0
            if self._first and not self._first.done():
                self._first.set_result(None)
            elif not first_schema and self.on_schema:
                # Reconnected: the entity list may have changed meanwhile.
                self.on_schema(self.entities, self.device)
            first_schema = False
            self._set_connected(True)
            self._notify(set(self.states))
            try:
                await self._read(self._ws)
            finally:
                self._set_connected(False)
                for fut in self._pending.values():
                    if not fut.done():
                        fut.set_exception(CannotConnect("disconnected"))
                self._pending.clear()
            await asyncio.sleep(backoff)

    async def _read(self, ws: aiohttp.ClientWebSocketResponse) -> None:
        async for msg in ws:
            if msg.type != aiohttp.WSMsgType.TEXT:
                break
            try:
                data = msg.json()
            except ValueError:
                continue
            kind = data.get("type")
            if kind == "states":
                changed = data.get("states") or {}
                self.states.update(changed)
                self._notify(set(changed))
            elif kind == "entities":
                self._apply_snapshot(data)
                if self.on_schema:
                    self.on_schema(self.entities, self.device)
                self._notify(set(self.states))
            elif kind == "result":
                fut = self._pending.pop(data.get("id"), None)
                if fut and not fut.done():
                    if data.get("ok"):
                        fut.set_result(None)
                    else:
                        fut.set_exception(CommandFailed(data.get("error", "failed")))
            elif kind == "event" and self.on_event:
                self.on_event(data.get("event", ""), data.get("data") or {})

    async def command(self, key: str, value: Any = None, timeout: float = 15) -> None:
        ws = self._ws
        if not self.connected or ws is None or ws.closed:
            raise CannotConnect("PC is offline")
        msg_id = next(self._ids)
        fut: asyncio.Future[None] = asyncio.get_running_loop().create_future()
        self._pending[msg_id] = fut
        try:
            await ws.send_json({"type": "command", "id": msg_id, "key": key, "value": value})
            await asyncio.wait_for(fut, timeout)
        except asyncio.TimeoutError as err:
            raise CommandFailed("PC did not answer") from err
        finally:
            self._pending.pop(msg_id, None)
