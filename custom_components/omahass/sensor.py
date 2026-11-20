"""PC sensors: CPU, memory, disks, battery, network, active window..."""

from __future__ import annotations

from datetime import datetime
from typing import Any

from homeassistant.components.sensor import SensorDeviceClass, SensorEntity, SensorStateClass
from homeassistant.core import HomeAssistant
from homeassistant.util import dt as dt_util

from . import OmahassConfigEntry
from .entity import OmahassEntity, entities_for


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry, async_add_entities) -> None:
    async_add_entities(entities_for(entry.runtime_data, "sensor", OmahassSensor))


class OmahassSensor(OmahassEntity, SensorEntity):
    def __init__(self, client, desc: dict[str, Any]) -> None:
        super().__init__(client, desc)
        if dc := desc.get("device_class"):
            try:
                self._attr_device_class = SensorDeviceClass(dc)
            except ValueError:
                pass
        if sc := desc.get("state_class"):
            try:
                self._attr_state_class = SensorStateClass(sc)
            except ValueError:
                pass
        if unit := desc.get("unit"):
            self._attr_native_unit_of_measurement = unit
        if (precision := desc.get("precision")) is not None:
            self._attr_suggested_display_precision = precision

    @property
    def native_value(self) -> Any:
        value = self.value
        if value is None:
            return None
        if self.device_class == SensorDeviceClass.TIMESTAMP:
            return dt_util.parse_datetime(str(value)) if not isinstance(value, datetime) else value
        if isinstance(value, str):
            return value[:255]
        return value
