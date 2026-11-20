"""PC switches: displays on/off, mute, microphone mute."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError

from . import OmahassConfigEntry
from .client import OmahassError
from .entity import OmahassEntity, entities_for


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry, async_add_entities) -> None:
    async_add_entities(entities_for(entry.runtime_data, "switch", OmahassSwitch))


class OmahassSwitch(OmahassEntity, SwitchEntity):
    @property
    def is_on(self) -> bool | None:
        value = self.value
        return None if value is None else bool(value)

    async def _set(self, on: bool) -> None:
        try:
            await self.client.command(self.key, on)
        except OmahassError as err:
            raise HomeAssistantError(f"{self.name}: {err}") from err

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._set(True)

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._set(False)
