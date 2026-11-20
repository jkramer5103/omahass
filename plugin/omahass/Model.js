.pragma library

// Pure helpers for the panel: icons, state formatting and which control an
// entity gets. No QML types here so it can be tested with plain node.

function glyph(cp) { return String.fromCodePoint(cp) }

var BRAND = glyph(0xF07D0)        // md-home_assistant
var STAR = glyph(0xF04CE)         // md-star
var STAR_OUTLINE = glyph(0xF04D2) // md-star_outline
var COG = glyph(0xF0493)          // md-cog
var LAPTOP = glyph(0xF0322)       // md-laptop
var HOME = glyph(0xF02DC)         // md-home
var DEVICES = glyph(0xF0FB0)      // md-devices
var SEARCH = glyph(0xF0349)       // md-magnify
var PLAY = glyph(0xF040A)
var PAUSE = glyph(0xF03E4)
var NEXT = glyph(0xF04AD)
var PREV = glyph(0xF04AE)
var UP = glyph(0xF0143)
var DOWN = glyph(0xF0140)
var STOP = glyph(0xF04DB)
var MINUS = glyph(0xF0374)
var PLUS = glyph(0xF0415)
var DELETE = glyph(0xF01B4)
var REFRESH = glyph(0xF0450)
var KEY = glyph(0xF0306)
var COPY = glyph(0xF018F)
var CONSOLE = glyph(0xF018D)
var RUN = glyph(0xF040A)

var DOMAIN_ICONS = {
  light: 0xF0335, switch: 0xF0521, fan: 0xF0210, input_boolean: 0xF0A1A,
  humidifier: 0xF1099, climate: 0xF0393, media_player: 0xF075A, cover: 0xF111C,
  lock: 0xF033E, scene: 0xF03D8, script: 0xF0BC2, sensor: 0xF029A,
  binary_sensor: 0xF0D91, automation: 0xF06A9, person: 0xF0004, weather: 0xF0595,
  button: 0xF0AE7, input_button: 0xF0AE7, vacuum: 0xF070D, camera: 0xF0567,
  number: 0xF0A0E, input_number: 0xF0A0E, select: 0xF0485, input_select: 0xF0485,
  device_tracker: 0xF034E, sun: 0xF05A8, update: 0xF06B0, notify: 0xF009A
}

var CLASS_ICONS = {
  temperature: 0xF050F, humidity: 0xF058E, battery: 0xF0079, power: 0xF0241,
  energy: 0xF0241, door: 0xF081A, window: 0xF05AE, motion: 0xF0D91,
  occupancy: 0xF0D91, illuminance: 0xF0335, plug: 0xF06A5, outlet: 0xF06A5,
  co2: 0xF07E4, carbon_dioxide: 0xF07E4, pm25: 0xF0F30, data_rate: 0xF06F3,
  timestamp: 0xF0954, battery_charging: 0xF0084, running: 0xF0425, tv: 0xF0502,
  speaker: 0xF04C3, garage: 0xF06D9, garage_door: 0xF06D9, shutter: 0xF111C,
  blind: 0xF00AC, curtain: 0xF1846
}

// mdi:foo-bar names the daemon sends for PC entities.
var MDI_ICONS = {
  "mdi:cpu-64-bit": 0xF0EE0, "mdi:memory": 0xF035B, "mdi:harddisk": 0xF02CA,
  "mdi:gauge": 0xF029A, "mdi:swap-horizontal": 0xF04E1, "mdi:fan": 0xF0210,
  "mdi:linux": 0xF033D, "mdi:information-outline": 0xF02FD, "mdi:ip-network": 0xF0A60,
  "mdi:wifi": 0xF05A9, "mdi:webcam": 0xF05A0, "mdi:microphone": 0xF036C,
  "mdi:music": 0xF075A, "mdi:application-outline": 0xF0614, "mdi:application": 0xF08C6,
  "mdi:view-grid-outline": 0xF11D9, "mdi:monitor": 0xF0379, "mdi:volume-high": 0xF057E,
  "mdi:volume-off": 0xF0581, "mdi:microphone-off": 0xF036D, "mdi:brightness-6": 0xF00DF,
  "mdi:lock": 0xF033E, "mdi:monitor-off": 0xF0D40, "mdi:power-sleep": 0xF0904,
  "mdi:snowflake": 0xF0717, "mdi:restart": 0xF0709, "mdi:power": 0xF0425,
  "mdi:logout": 0xF0343, "mdi:message-badge": 0xF1941, "mdi:console": 0xF018D
}

function pcIcon(e) {
  if (e.icon && MDI_ICONS[e.icon] !== undefined) return glyph(MDI_ICONS[e.icon])
  if (e.device_class && CLASS_ICONS[e.device_class] !== undefined) return glyph(CLASS_ICONS[e.device_class])
  if (e.platform === "button") return CONSOLE
  return glyph(0xF029A)
}

function attr(e, k) { return e && e.attributes ? e.attributes[k] : undefined }

function icon(e) {
  if (!e) return DEVICES
  var dc = attr(e, "device_class")
  if (e.domain === "lock") return glyph(e.state === "locked" ? 0xF033E : 0xF033F)
  if (e.domain === "light" && e.state !== "on") return glyph(0xF0336)
  if ((e.domain === "sensor" || e.domain === "binary_sensor" || e.domain === "cover" || e.domain === "switch")
      && dc && CLASS_ICONS[dc] !== undefined) return glyph(CLASS_ICONS[dc])
  if (DOMAIN_ICONS[e.domain] !== undefined) return glyph(DOMAIN_ICONS[e.domain])
  return DEVICES
}

var TOGGLE_DOMAINS = ["light", "switch", "fan", "input_boolean", "humidifier", "automation", "siren"]

// kind decides the trailing control in a row.
function kind(e) {
  if (!e) return "readonly"
  if (TOGGLE_DOMAINS.indexOf(e.domain) >= 0) return "toggle"
  if (e.domain === "lock") return "lock"
  if (e.domain === "scene" || e.domain === "script") return "run"
  if (e.domain === "button" || e.domain === "input_button") return "press"
  if (e.domain === "media_player") return "media"
  if (e.domain === "cover") return "cover"
  if (e.domain === "climate") return "climate"
  return "readonly"
}

function isOn(e) {
  if (!e) return false
  if (e.domain === "lock") return e.state === "locked"
  if (e.domain === "climate") return e.state && e.state !== "off" && e.state !== "unavailable"
  return e.state === "on" || e.state === "open" || e.state === "playing"
}

function unavailable(e) { return !e || e.state === "unavailable" || e.state === "unknown" }

// The service call that flips an entity: [domain, service, data].
function toggleCall(e) {
  switch (e.domain) {
  case "lock": return ["lock", e.state === "locked" ? "unlock" : "lock", {}]
  case "scene": return ["scene", "turn_on", {}]
  case "script": return ["script", "turn_on", {}]
  case "button": return ["button", "press", {}]
  case "input_button": return ["input_button", "press", {}]
  case "media_player": return ["media_player", "media_play_pause", {}]
  }
  return [e.domain, isOn(e) ? "turn_off" : "turn_on", {}]
}

function dimmable(e) {
  if (!e || e.domain !== "light") return false
  var modes = attr(e, "supported_color_modes") || []
  for (var i = 0; i < modes.length; i++) if (modes[i] !== "onoff") return true
  return false
}

function brightnessPct(e) {
  var b = attr(e, "brightness")
  return typeof b === "number" ? Math.round(b / 2.55) : 0
}

function titleCase(s) {
  s = String(s || "").replace(/_/g, " ")
  return s.length ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

function fmtNumber(v, decimals) {
  var n = Number(v)
  if (!isFinite(n)) return String(v)
  if (decimals === undefined) decimals = Math.abs(n) >= 100 || Number.isInteger(n) ? 0 : 1
  return n.toFixed(decimals)
}

// Short state text shown under the entity name.
function stateText(e) {
  if (!e) return ""
  if (e.state === "unavailable") return "Unavailable"
  if (e.state === "unknown") return "Unknown"
  var unit = attr(e, "unit_of_measurement")
  switch (e.domain) {
  case "light":
    if (e.state === "on" && typeof attr(e, "brightness") === "number") return "On · " + brightnessPct(e) + "%"
    return titleCase(e.state)
  case "media_player":
    var title = attr(e, "media_title")
    if (title && (e.state === "playing" || e.state === "paused")) {
      var artist = attr(e, "media_artist")
      return artist ? title + " — " + artist : title
    }
    return titleCase(e.state)
  case "climate":
    var cur = attr(e, "current_temperature"), target = attr(e, "temperature")
    var parts = [titleCase(e.state)]
    if (cur !== undefined && cur !== null) parts.push(fmtNumber(cur) + "°")
    if (target !== undefined && target !== null) parts.push("→ " + fmtNumber(target) + "°")
    return parts.join(" · ")
  case "cover":
    var pos = attr(e, "current_position")
    return titleCase(e.state) + (typeof pos === "number" ? " · " + pos + "%" : "")
  case "scene": case "script": case "button": case "input_button":
    return ""
  case "binary_sensor":
    return e.state === "on" ? "Detected" : "Clear"
  }
  if (unit !== undefined && !isNaN(Number(e.state))) return fmtNumber(e.state) + " " + unit
  if (attr(e, "device_class") === "timestamp") return relTime(e.state)
  return titleCase(e.state)
}

function relTime(iso) {
  var t = Date.parse(iso)
  if (isNaN(t)) return String(iso)
  var mins = Math.round((Date.now() - t) / 60000)
  if (mins < 1) return "just now"
  if (mins < 60) return mins + " min ago"
  var h = Math.round(mins / 60)
  if (h < 48) return h + " h ago"
  return Math.round(h / 24) + " days ago"
}

// Value text for a PC entity in the This PC view.
function pcValue(e, v) {
  if (v === undefined || v === null) return e.platform === "button" || e.platform === "notify" ? "" : "—"
  if (e.platform === "binary_sensor") {
    if (e.device_class === "battery_charging") return v ? "Charging" : "Not charging"
    if (e.device_class === "plug") return v ? "Yes" : "No"
    return v ? "Yes" : "No"
  }
  if (e.platform === "switch") return v ? "On" : "Off"
  if (e.platform === "media_player") {
    if (!v || v.state === "off") return "Nothing playing"
    var t = v.title ? v.title + (v.artist ? " — " + v.artist : "") : titleCase(v.state)
    return (v.player ? v.player + ": " : "") + t
  }
  if (e.device_class === "timestamp") return relTime(v)
  if (typeof v === "number") {
    var d = e.precision !== undefined ? e.precision : undefined
    return fmtNumber(v, d) + (e.unit ? (e.unit === "%" ? "%" : " " + e.unit) : "")
  }
  return String(v)
}

var DOMAIN_LABELS = {
  all: "All", light: "Lights", switch: "Switches", sensor: "Sensors",
  binary_sensor: "Binary", media_player: "Media", climate: "Climate",
  cover: "Covers", scene: "Scenes", script: "Scripts", fan: "Fans",
  lock: "Locks", automation: "Automations", button: "Buttons",
  input_boolean: "Helpers", person: "People", vacuum: "Vacuums"
}

// The filter chips: "all" plus the most useful domains present.
function domainChips(counts) {
  var order = ["light", "switch", "media_player", "climate", "cover", "fan", "lock", "scene", "script", "sensor", "binary_sensor", "input_boolean", "button", "automation"]
  var out = ["all"]
  for (var i = 0; i < order.length; i++) if (counts && counts[order[i]] > 0) out.push(order[i])
  return out
}

function domainLabel(d) { return DOMAIN_LABELS[d] || titleCase(d) }

function groupPc(entities) {
  var groups = []
  var index = {}
  for (var i = 0; i < entities.length; i++) {
    var g = entities[i].group || "Other"
    if (index[g] === undefined) { index[g] = groups.length; groups.push({ name: g, entities: [] }) }
    groups[index[g]].entities.push(entities[i])
  }
  return groups
}

function phaseText(daemonRunning, ha) {
  if (!daemonRunning) return "Daemon not running"
  if (!ha) return ""
  switch (ha.phase) {
  case "connected": return ha.name ? "Connected · " + ha.name : "Connected"
  case "connecting": return "Connecting…"
  case "unconfigured": return "Not set up"
  case "error": return ha.error || "Error"
  }
  return ha.phase || ""
}
