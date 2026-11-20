"""A tiny fake Home Assistant (WebSocket API only) with demo devices.

    python fake_ha.py [port]        # token: "demo"

Used for previews and manual testing of the bar panel without a real
instance. Needs aiohttp.
"""

import asyncio
import json
import sys

from aiohttp import web

STATES = {
    "light.ceiling": {"state": "on", "attributes": {"friendly_name": "Ceiling Light", "brightness": 178, "supported_color_modes": ["color_temp"], "color_mode": "color_temp"}},
    "light.floor": {"state": "off", "attributes": {"friendly_name": "Floor Lamp", "supported_color_modes": ["onoff"]}},
    "light.desk": {"state": "off", "attributes": {"friendly_name": "Schreibtischlampe", "supported_color_modes": ["brightness"]}},
    "switch.coffee": {"state": "on", "attributes": {"friendly_name": "Coffee machine", "device_class": "outlet"}},
    "sensor.plant_temp": {"state": "18.4820175170898", "attributes": {"friendly_name": "Plant temperature", "unit_of_measurement": "°C", "device_class": "temperature"}},
    "sensor.weather_temp": {"state": "4.0", "attributes": {"friendly_name": "Outdoor temperature", "unit_of_measurement": "°C", "device_class": "temperature"}},
    "sensor.humidity": {"state": "48", "attributes": {"friendly_name": "Living room humidity", "unit_of_measurement": "%", "device_class": "humidity"}},
    "binary_sensor.front_door": {"state": "off", "attributes": {"friendly_name": "Front door", "device_class": "door"}},
    "media_player.living_room": {"state": "playing", "attributes": {"friendly_name": "Living room speaker", "media_title": "Midnight City", "media_artist": "M83", "volume_level": 0.35}},
    "climate.hallway": {"state": "heat", "attributes": {"friendly_name": "Hallway thermostat", "current_temperature": 20.5, "temperature": 21, "target_temp_step": 0.5, "hvac_modes": ["off", "heat"]}},
    "cover.blinds": {"state": "open", "attributes": {"friendly_name": "Office blinds", "current_position": 70}},
    "scene.movie": {"state": "2026-11-20T00:00:00+00:00", "attributes": {"friendly_name": "Movie night"}},
    "lock.front": {"state": "locked", "attributes": {"friendly_name": "Front door lock"}},
}


def entity(eid):
    s = STATES[eid]
    return {"entity_id": eid, "state": s["state"], "attributes": s["attributes"]}


async def ws_handler(request):
    ws = web.WebSocketResponse()
    await ws.prepare(request)
    await ws.send_json({"type": "auth_required"})
    auth = await ws.receive_json()
    if auth.get("access_token") != "demo":
        await ws.send_json({"type": "auth_invalid"})
        await ws.close()
        return ws
    await ws.send_json({"type": "auth_ok"})
    sub_id = None
    async for msg in ws:
        m = json.loads(msg.data)
        t, mid = m.get("type"), m.get("id")
        if t == "get_states":
            await ws.send_json({"id": mid, "type": "result", "success": True, "result": [entity(e) for e in STATES]})
        elif t == "get_config":
            await ws.send_json({"id": mid, "type": "result", "success": True, "result": {"location_name": "Home"}})
        elif t == "subscribe_events":
            sub_id = mid
            await ws.send_json({"id": mid, "type": "result", "success": True, "result": None})
        elif t == "call_service":
            eid = (m.get("target") or {}).get("entity_id")
            svc = m.get("service")
            if eid in STATES:
                s = STATES[eid]
                if svc in ("turn_on", "turn_off"):
                    s["state"] = "on" if svc == "turn_on" else "off"
                    if "brightness_pct" in m.get("service_data", {}):
                        s["attributes"]["brightness"] = round(m["service_data"]["brightness_pct"] * 2.55)
                elif svc == "toggle":
                    s["state"] = "off" if s["state"] == "on" else "on"
                elif svc == "media_play_pause":
                    s["state"] = "paused" if s["state"] == "playing" else "playing"
                elif svc in ("lock", "unlock"):
                    s["state"] = "locked" if svc == "lock" else "unlocked"
                elif svc == "set_temperature":
                    s["attributes"]["temperature"] = m["service_data"]["temperature"]
            await ws.send_json({"id": mid, "type": "result", "success": True, "result": None})
            if eid in STATES and sub_id is not None:
                await ws.send_json({"id": sub_id, "type": "event", "event": {"event_type": "state_changed", "data": {"entity_id": eid, "new_state": entity(eid)}}})
        else:
            await ws.send_json({"id": mid, "type": "result", "success": True, "result": None})
    return ws


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8999
    app = web.Application()
    app.router.add_get("/api/websocket", ws_handler)
    web.run_app(app, host="127.0.0.1", port=port, print=lambda *_: None)


if __name__ == "__main__":
    main()
