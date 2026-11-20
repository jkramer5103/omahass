from __future__ import annotations

import asyncio
from ipaddress import ip_address

from homeassistant import config_entries
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.core import HomeAssistant
from homeassistant.data_entry_flow import FlowResultType
from homeassistant.helpers import device_registry as dr, entity_registry as er
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo
from pytest_homeassistant_custom_component.common import MockConfigEntry

from custom_components.omahass.const import CONF_KEY, DOMAIN


async def _wait(cond, timeout=8.0):
    loop = asyncio.get_running_loop()
    end = loop.time() + timeout
    while loop.time() < end:
        if cond():
            return
        await asyncio.sleep(0.1)
    raise AssertionError("condition not met in time")


async def test_user_flow(hass: HomeAssistant, daemon) -> None:
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": config_entries.SOURCE_USER})
    assert result["type"] is FlowResultType.FORM

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: daemon.host, CONF_PORT: daemon.port, CONF_KEY: "wrong"}
    )
    assert result["errors"] == {"base": "invalid_key"}

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: daemon.host, CONF_PORT: 1, CONF_KEY: daemon.key}
    )
    assert result["errors"] == {"base": "cannot_connect"}

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"], {CONF_HOST: daemon.host, CONF_PORT: daemon.port, CONF_KEY: daemon.key}
    )
    assert result["type"] is FlowResultType.CREATE_ENTRY
    assert result["title"] == "Test PC"
    await hass.async_block_till_done()
    await _wait(lambda: hass.states.get("sensor.test_pc_cpu_usage") is not None)


async def test_zeroconf_flow(hass: HomeAssistant, daemon) -> None:
    info = ZeroconfServiceInfo(
        ip_address=ip_address(daemon.host), ip_addresses=[ip_address(daemon.host)],
        hostname="testpc.local.", name="Test PC._omahass._tcp.local.", port=daemon.port,
        type="_omahass._tcp.local.", properties={"id": daemon.device_id(), "name": "Test PC"},
    )
    result = await hass.config_entries.flow.async_init(DOMAIN, context={"source": config_entries.SOURCE_ZEROCONF}, data=info)
    assert result["type"] is FlowResultType.FORM and result["step_id"] == "pair"
    result = await hass.config_entries.flow.async_configure(result["flow_id"], {CONF_KEY: "nope"})
    assert result["errors"] == {"base": "invalid_key"}
    result = await hass.config_entries.flow.async_configure(result["flow_id"], {CONF_KEY: daemon.key})
    assert result["type"] is FlowResultType.CREATE_ENTRY
    await hass.async_block_till_done()

    # Discovering it again aborts as already configured.
    again = await hass.config_entries.flow.async_init(DOMAIN, context={"source": config_entries.SOURCE_ZEROCONF}, data=info)
    assert again["type"] is FlowResultType.ABORT and again["reason"] == "already_configured"


async def _setup(hass: HomeAssistant, daemon) -> MockConfigEntry:
    entry = MockConfigEntry(
        domain=DOMAIN, unique_id=daemon.device_id(), title="Test PC",
        data={CONF_HOST: daemon.host, CONF_PORT: daemon.port, CONF_KEY: daemon.key},
    )
    entry.add_to_hass(hass)
    assert await hass.config_entries.async_setup(entry.entry_id)
    await hass.async_block_till_done()
    return entry


async def test_entities_and_device(hass: HomeAssistant, daemon) -> None:
    entry = await _setup(hass, daemon)
    assert entry.state is config_entries.ConfigEntryState.LOADED

    cpu = hass.states.get("sensor.test_pc_cpu_usage")
    assert cpu is not None and cpu.attributes["unit_of_measurement"] == "%"
    await _wait(lambda: hass.states.get("sensor.test_pc_cpu_usage").state not in ("unknown", "unavailable"))
    assert hass.states.get("button.test_pc_lock") is not None
    assert hass.states.get("media_player.test_pc_media") is not None
    assert hass.states.get("notify.test_pc_notifications") is not None
    assert hass.states.get("number.test_pc_volume") is not None
    assert hass.states.get("switch.test_pc_displays") is not None
    assert hass.states.get("binary_sensor.test_pc_webcam_in_use") is not None
    # Off by default: dangerous or noisy.
    assert hass.states.get("button.test_pc_shut_down") is None
    assert hass.states.get("sensor.test_pc_kernel") is None

    device = dr.async_get(hass).async_get_device(identifiers={(DOMAIN, entry.unique_id)})
    assert device is not None and device.name == "Test PC"
    assert len(er.async_entries_for_device(er.async_get(hass), device.id)) > 15


async def test_expose_toggle_reloads(hass: HomeAssistant, daemon) -> None:
    await _setup(hass, daemon)
    daemon.ui({"op": "expose", "key": "kernel", "on": True})
    await _wait(lambda: hass.states.get("sensor.test_pc_kernel") is not None)
    daemon.ui({"op": "expose", "key": "kernel", "on": False})
    await _wait(lambda: (s := hass.states.get("sensor.test_pc_kernel")) is None or s.state == "unavailable")


async def test_custom_command_button(hass: HomeAssistant, daemon) -> None:
    marker = daemon.tmp / "pressed"
    daemon.ui({"op": "add_command", "name": "Touch marker", "command": f"touch {marker}"})
    await _setup(hass, daemon)
    await _wait(lambda: hass.states.get("button.test_pc_touch_marker") is not None)
    await hass.services.async_call("button", "press", {"entity_id": "button.test_pc_touch_marker"}, blocking=True)
    await _wait(lambda: marker.exists())


async def test_offline_then_back(hass: HomeAssistant, daemon) -> None:
    entry = await _setup(hass, daemon)
    client = entry.runtime_data
    await client._ws.close()  # simulate a dropped connection
    await _wait(lambda: hass.states.get("sensor.test_pc_cpu_usage").state == "unavailable", 3)
    await _wait(lambda: hass.states.get("sensor.test_pc_cpu_usage").state != "unavailable", 10)
