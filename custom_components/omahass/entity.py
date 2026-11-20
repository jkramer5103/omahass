"""Base entity shared by every platform."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from homeassistant.const import EntityCategory
from homeassistant.core import callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.entity import Entity

from .client import OmahassClient
from .const import DOMAIN


def entities_for(client: OmahassClient, platform: str, cls: Callable[..., Entity]) -> list[Entity]:
    return [cls(client, d) for d in client.entities if d.get("platform") == platform]


class OmahassEntity(Entity):
    """One PC entity, updated by pushes from the daemon."""

    _attr_has_entity_name = True
    _attr_should_poll = False

    def __init__(self, client: OmahassClient, desc: dict[str, Any]) -> None:
        self.client = client
        self.desc = desc
        self.key: str = desc["key"]
        device = client.device
        self._attr_unique_id = f"{device.get('id')}_{self.key}"
        self._attr_name = desc.get("name") or self.key
        if icon := desc.get("icon"):
            self._attr_icon = icon
        if category := desc.get("entity_category"):
            try:
                self._attr_entity_category = EntityCategory(category)
            except ValueError:
                pass
        self._attr_device_info = DeviceInfo(identifiers={(DOMAIN, device.get("id"))})

    @property
    def value(self) -> Any:
        return self.client.states.get(self.key)

    @property
    def available(self) -> bool:
        return self.client.connected

    async def async_added_to_hass(self) -> None:
        self.async_on_remove(self.client.add_listener(self._pushed))

    @callback
    def _pushed(self, keys: set[str] | None) -> None:
        if keys is None or self.key in keys:
            self.async_write_ha_state()
