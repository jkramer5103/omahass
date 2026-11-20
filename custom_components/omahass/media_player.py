"""Whatever MPRIS player is active on the PC (Spotify, browser, mpv...)."""

from __future__ import annotations

import hashlib
from datetime import datetime
from typing import Any

from homeassistant.components.media_player import (
    MediaPlayerEntity,
    MediaPlayerEntityFeature,
    MediaPlayerState,
    MediaType,
)
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.util import dt as dt_util

from . import OmahassConfigEntry
from .client import OmahassError
from .entity import OmahassEntity, entities_for

STATES = {
    "playing": MediaPlayerState.PLAYING,
    "paused": MediaPlayerState.PAUSED,
    "idle": MediaPlayerState.IDLE,
    "off": MediaPlayerState.OFF,
}


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry, async_add_entities) -> None:
    async_add_entities(entities_for(entry.runtime_data, "media_player", OmahassMediaPlayer))


class OmahassMediaPlayer(OmahassEntity, MediaPlayerEntity):
    _attr_media_content_type = MediaType.MUSIC
    _attr_media_image_remotely_accessible = False  # HA proxies it from the PC
    _attr_supported_features = (
        MediaPlayerEntityFeature.PLAY
        | MediaPlayerEntityFeature.PAUSE
        | MediaPlayerEntityFeature.STOP
        | MediaPlayerEntityFeature.NEXT_TRACK
        | MediaPlayerEntityFeature.PREVIOUS_TRACK
        | MediaPlayerEntityFeature.VOLUME_SET
        | MediaPlayerEntityFeature.SEEK
        | MediaPlayerEntityFeature.SHUFFLE_SET
    )

    @property
    def _m(self) -> dict[str, Any]:
        value = self.value
        return value if isinstance(value, dict) else {}

    @property
    def state(self) -> MediaPlayerState | None:
        return STATES.get(self._m.get("state", "off"), MediaPlayerState.IDLE)

    @property
    def media_title(self) -> str | None:
        return self._m.get("title") or None

    @property
    def media_artist(self) -> str | None:
        return self._m.get("artist") or None

    @property
    def media_album_name(self) -> str | None:
        return self._m.get("album") or None

    @property
    def app_name(self) -> str | None:
        return self._m.get("player") or None

    @property
    def media_duration(self) -> int | None:
        d = self._m.get("duration")
        return int(d) if d else None

    @property
    def media_position(self) -> int | None:
        p = self._m.get("position")
        return int(p) if p is not None and self._m.get("duration") else None

    @property
    def media_position_updated_at(self) -> datetime | None:
        at = self._m.get("position_updated_at")
        return dt_util.parse_datetime(at) if at else None

    @property
    def volume_level(self) -> float | None:
        return self._m.get("volume")

    @property
    def shuffle(self) -> bool | None:
        return self._m.get("shuffle")

    @property
    def media_image_url(self) -> str | None:
        art = self._m.get("art_url")
        if not art:
            return None
        # The URL changes with the track so HA's image cache follows along.
        digest = hashlib.sha1(art.encode()).hexdigest()[:12]
        return f"{self.client.art_url()}&v={digest}"

    @property
    def media_image_hash(self) -> str | None:
        art = self._m.get("art_url")
        return hashlib.sha1(art.encode()).hexdigest()[:16] if art else None

    async def _cmd(self, action: str, value: Any = None) -> None:
        try:
            await self.client.command(self.key, {"action": action, "value": value})
        except OmahassError as err:
            raise HomeAssistantError(f"Media: {err}") from err

    async def async_media_play(self) -> None:
        await self._cmd("play")

    async def async_media_pause(self) -> None:
        await self._cmd("pause")

    async def async_media_play_pause(self) -> None:
        await self._cmd("play_pause")

    async def async_media_stop(self) -> None:
        await self._cmd("stop")

    async def async_media_next_track(self) -> None:
        await self._cmd("next")

    async def async_media_previous_track(self) -> None:
        await self._cmd("previous")

    async def async_set_volume_level(self, volume: float) -> None:
        await self._cmd("volume", volume)

    async def async_media_seek(self, position: float) -> None:
        await self._cmd("seek", position)

    async def async_set_shuffle(self, shuffle: bool) -> None:
        await self._cmd("shuffle", shuffle)
