"""Config flow: zeroconf discovery or manual host, then the pairing key."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.helpers.aiohttp_client import async_get_clientsession

try:
    from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo
except ImportError:  # Home Assistant < 2025.1
    from homeassistant.components.zeroconf import ZeroconfServiceInfo

from .client import CannotConnect, InvalidKey, OmahassClient
from .const import CONF_KEY, DEFAULT_PORT, DOMAIN


class OmahassConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle a config flow for Omarchy PC."""

    VERSION = 1

    def __init__(self) -> None:
        self._host: str | None = None
        self._port: int = DEFAULT_PORT
        self._name: str = "Omarchy PC"

    async def _validate(self, host: str, port: int, key: str) -> tuple[dict[str, Any] | None, str | None]:
        client = OmahassClient(async_get_clientsession(self.hass), host, port, key)
        try:
            return await client.probe(), None
        except InvalidKey:
            return None, "invalid_key"
        except CannotConnect:
            return None, "cannot_connect"
        except Exception:  # noqa: BLE001
            return None, "unknown"

    async def _create(self, host: str, port: int, key: str, device: dict[str, Any]) -> ConfigFlowResult:
        await self.async_set_unique_id(device["id"])
        self._abort_if_unique_id_configured(updates={CONF_HOST: host, CONF_PORT: port, CONF_KEY: key})
        return self.async_create_entry(
            title=device.get("name") or self._name,
            data={CONF_HOST: host, CONF_PORT: port, CONF_KEY: key},
        )

    async def async_step_user(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            host = user_input[CONF_HOST].strip()
            port = int(user_input[CONF_PORT])
            key = user_input[CONF_KEY].strip()
            device, error = await self._validate(host, port, key)
            if device is not None:
                return await self._create(host, port, key, device)
            errors["base"] = error or "unknown"
        schema = vol.Schema(
            {
                vol.Required(CONF_HOST, default=(user_input or {}).get(CONF_HOST, "")): str,
                vol.Required(CONF_PORT, default=DEFAULT_PORT): int,
                vol.Required(CONF_KEY): str,
            }
        )
        return self.async_show_form(step_id="user", data_schema=schema, errors=errors)

    async def async_step_zeroconf(self, discovery_info: ZeroconfServiceInfo) -> ConfigFlowResult:
        props = discovery_info.properties
        device_id = props.get("id")
        if not device_id:
            return self.async_abort(reason="unknown")
        self._host = str(discovery_info.host)
        self._port = discovery_info.port or DEFAULT_PORT
        self._name = props.get("name") or self._name
        await self.async_set_unique_id(device_id)
        # Known PC that moved to a new address: follow it silently.
        self._abort_if_unique_id_configured(updates={CONF_HOST: self._host, CONF_PORT: self._port})
        self.context["title_placeholders"] = {"name": self._name}
        return await self.async_step_pair()

    async def async_step_pair(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        assert self._host is not None
        errors: dict[str, str] = {}
        if user_input is not None:
            key = user_input[CONF_KEY].strip()
            device, error = await self._validate(self._host, self._port, key)
            if device is not None:
                return await self._create(self._host, self._port, key, device)
            errors["base"] = error or "unknown"
        return self.async_show_form(
            step_id="pair",
            data_schema=vol.Schema({vol.Required(CONF_KEY): str}),
            errors=errors,
            description_placeholders={"name": self._name, "host": f"{self._host}:{self._port}"},
        )

    async def async_step_reauth(self, entry_data: Mapping[str, Any]) -> ConfigFlowResult:
        self._host = entry_data[CONF_HOST]
        self._port = entry_data[CONF_PORT]
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        assert self._host is not None
        entry = self._get_reauth_entry()
        errors: dict[str, str] = {}
        if user_input is not None:
            key = user_input[CONF_KEY].strip()
            device, error = await self._validate(self._host, self._port, key)
            if device is not None:
                return self.async_update_reload_and_abort(entry, data_updates={CONF_KEY: key})
            errors["base"] = error or "unknown"
        return self.async_show_form(
            step_id="reauth_confirm",
            data_schema=vol.Schema({vol.Required(CONF_KEY): str}),
            errors=errors,
            description_placeholders={"name": entry.title},
        )
