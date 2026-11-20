"""PC sliders: volume, screen brightness."""

from __future__ import annotations

from typing import Any

from homeassistant.components.number import NumberEntity, NumberMode
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError

from . import OmahassConfigEntry
from .client import OmahassError
from .entity import OmahassEntity, entities_for


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry, async_add_entities) -> None:
    async_add_entities(entities_for(entry.runtime_data, "number", OmahassNumber))


class OmahassNumber(OmahassEntity, NumberEntity):
    _attr_mode = NumberMode.SLIDER

    def __init__(self, client, desc: dict[str, Any]) -> None:
        super().__init__(client, desc)
        self._attr_native_min_value = float(desc.get("min", 0))
        self._attr_native_max_value = float(desc.get("max", 100))
        self._attr_native_step = float(desc.get("step", 1))
        if unit := desc.get("unit"):
            self._attr_native_unit_of_measurement = unit

    @property
    def native_value(self) -> float | None:
        value = self.value
        return None if value is None else float(value)

    async def async_set_native_value(self, value: float) -> None:
        try:
            await self.client.command(self.key, value)
        except OmahassError as err:
            raise HomeAssistantError(f"{self.name}: {err}") from err
