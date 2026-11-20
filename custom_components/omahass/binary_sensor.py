"""PC binary sensors: charging, plugged in, webcam/mic in use."""

from __future__ import annotations

from typing import Any

from homeassistant.components.binary_sensor import BinarySensorDeviceClass, BinarySensorEntity
from homeassistant.core import HomeAssistant

from . import OmahassConfigEntry
from .entity import OmahassEntity, entities_for


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry, async_add_entities) -> None:
    async_add_entities(entities_for(entry.runtime_data, "binary_sensor", OmahassBinarySensor))


class OmahassBinarySensor(OmahassEntity, BinarySensorEntity):
    def __init__(self, client, desc: dict[str, Any]) -> None:
        super().__init__(client, desc)
        if dc := desc.get("device_class"):
            try:
                self._attr_device_class = BinarySensorDeviceClass(dc)
            except ValueError:
                pass

    @property
    def is_on(self) -> bool | None:
        value = self.value
        return None if value is None else bool(value)
