"""PC command buttons: lock, sleep, restart, custom commands..."""

from __future__ import annotations

from typing import Any

from homeassistant.components.button import ButtonDeviceClass, ButtonEntity
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError

from . import OmahassConfigEntry
from .client import OmahassError
from .entity import OmahassEntity, entities_for


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry, async_add_entities) -> None:
    async_add_entities(entities_for(entry.runtime_data, "button", OmahassButton))


class OmahassButton(OmahassEntity, ButtonEntity):
    def __init__(self, client, desc: dict[str, Any]) -> None:
        super().__init__(client, desc)
        if dc := desc.get("device_class"):
            try:
                self._attr_device_class = ButtonDeviceClass(dc)
            except ValueError:
                pass

    async def async_press(self) -> None:
        try:
            await self.client.command(self.key)
        except OmahassError as err:
            raise HomeAssistantError(f"{self.name}: {err}") from err
