"""Desktop notifications on the PC, optionally with action buttons."""

from __future__ import annotations

from typing import Any

import voluptuous as vol

from homeassistant.components.notify import NotifyEntity, NotifyEntityFeature
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import config_validation as cv, entity_platform

from . import OmahassConfigEntry
from .client import OmahassError
from .entity import OmahassEntity, entities_for

ACTION_SCHEMA = vol.Schema({vol.Required("action"): cv.string, vol.Required("title"): cv.string})


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry, async_add_entities) -> None:
    async_add_entities(entities_for(entry.runtime_data, "notify", OmahassNotify))
    platform = entity_platform.async_get_current_platform()
    platform.async_register_entity_service(
        "send_actionable",
        {
            vol.Required("message"): cv.string,
            vol.Optional("title"): cv.string,
            vol.Optional("actions", default=[]): vol.All(cv.ensure_list, [ACTION_SCHEMA]),
            vol.Optional("tag"): cv.string,
            vol.Optional("urgency"): vol.In(["low", "normal", "critical"]),
        },
        "async_send_actionable",
    )


class OmahassNotify(OmahassEntity, NotifyEntity):
    _attr_supported_features = NotifyEntityFeature.TITLE

    async def _send(self, payload: dict[str, Any]) -> None:
        try:
            await self.client.command(self.key, payload)
        except OmahassError as err:
            raise HomeAssistantError(f"Notification: {err}") from err

    async def async_send_message(self, message: str, title: str | None = None) -> None:
        await self._send({"message": message, "title": title or ""})

    async def async_send_actionable(
        self,
        message: str,
        title: str | None = None,
        actions: list[dict[str, str]] | None = None,
        tag: str | None = None,
        urgency: str | None = None,
    ) -> None:
        await self._send(
            {
                "message": message,
                "title": title or "",
                "actions": actions or [],
                "tag": tag or "",
                "urgency": urgency or "",
            }
        )
