"""Omarchy PC: an Omarchy desktop as a Home Assistant device, via omahassd."""

from __future__ import annotations

import logging
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import ConfigEntryAuthFailed, ConfigEntryNotReady
from homeassistant.helpers import device_registry as dr
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .client import CannotConnect, InvalidKey, OmahassClient
from .const import CONF_KEY, DOMAIN, EVENT_NOTIFICATION_ACTION, PLATFORMS

_LOGGER = logging.getLogger(__name__)

type OmahassConfigEntry = ConfigEntry[OmahassClient]


def _signature(entities: list[dict[str, Any]], device: dict[str, Any]) -> tuple:
    """What, if changed, requires re-creating entities."""
    return (
        device.get("name"),
        tuple(
            (e.get("key"), e.get("platform"), e.get("name"), e.get("unit"), e.get("device_class"))
            for e in entities
        ),
    )


async def async_setup_entry(hass: HomeAssistant, entry: OmahassConfigEntry) -> bool:
    client = OmahassClient(
        async_get_clientsession(hass),
        entry.data[CONF_HOST],
        entry.data[CONF_PORT],
        entry.data[CONF_KEY],
    )
    try:
        await client.start()
    except InvalidKey as err:
        raise ConfigEntryAuthFailed("pairing key rejected") from err
    except (CannotConnect, TimeoutError) as err:
        raise ConfigEntryNotReady(f"PC at {client.url} unreachable") from err

    signature = _signature(client.entities, client.device)

    @callback
    def schema_changed(entities: list[dict[str, Any]], device: dict[str, Any]) -> None:
        # Entities were toggled in the bar panel (or the PC was renamed):
        # rebuild them by reloading the entry.
        if _signature(entities, device) != signature:
            _LOGGER.debug("PC %s changed its entity list, reloading", device.get("name"))
            hass.config_entries.async_schedule_reload(entry.entry_id)

    @callback
    def event(name: str, data: dict[str, Any]) -> None:
        if name == "notification_action":
            hass.bus.async_fire(
                EVENT_NOTIFICATION_ACTION,
                {**data, "device_id": client.device.get("id"), "device_name": client.device.get("name")},
            )

    client.on_schema = schema_changed
    client.on_event = event

    dev = client.device
    dr.async_get(hass).async_get_or_create(
        config_entry_id=entry.entry_id,
        identifiers={(DOMAIN, dev.get("id", entry.entry_id))},
        name=dev.get("name"),
        manufacturer=dev.get("manufacturer") or None,
        model=dev.get("model") or None,
        sw_version=dev.get("sw_version"),
        configuration_url=None,
    )

    entry.runtime_data = client
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: OmahassConfigEntry) -> bool:
    unloaded = await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
    if unloaded:
        await entry.runtime_data.stop()
    return unloaded
