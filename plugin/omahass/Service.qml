import QtQuick
import Quickshell
import Quickshell.Io

// Session-wide state for the panel. Mounted once; bar widgets (one per
// monitor) reach it through bar.shell.serviceFor("omahass").
//
// All Home Assistant and PC logic lives in omahassd. This only relays NDJSON
// through `omahassd attach`, which reconnects to the daemon on its own.
QtObject {
  id: root

  property string executable: "omahassd"

  property bool daemonRunning: false
  property bool attachFailed: false
  property var ha: ({ phase: "idle", error: "", name: "", url: "", has_token: false })
  property var agent: ({ port: 0, key: "", device_name: "", device_id: "", clients: [], ip: "" })

  // Favorites with their live states, in display order.
  property var favorites: []
  property var favoriteIds: []

  // This PC: entity descriptions plus a separate state map so a sensor tick
  // doesn't rebuild the description list.
  property var pcEntities: []
  property var pcStates: ({})
  property int pcRevision: 0
  property var customCommands: []

  // Device picker results.
  property var listEntities: []
  property var listDomains: ({})
  property bool listLoading: false

  property string lastError: ""
  property int tagCounter: 0

  readonly property bool connected: daemonRunning && ha.phase === "connected"

  signal resultArrived(string tag, bool ok, string error)

  function send(op) {
    if (!attach.running) return false
    attach.write(JSON.stringify(op) + "\n")
    return true
  }

  function nextTag() { tagCounter += 1; return "t" + tagCounter }

  function call(domain, service, entityId, data) {
    return send({ op: "call", domain: domain, service: service, entity_id: entityId, data: data || {}, tag: nextTag() })
  }
  function list(query, domain) {
    listLoading = true
    send({ op: "list", query: query || "", domain: domain || "all" })
  }
  function setFavorite(entityId, on) { send({ op: "favorite", entity_id: entityId, on: on }) }
  function moveFavorite(entityId, delta) { send({ op: "move_favorite", entity_id: entityId, delta: delta }) }
  function setHa(url, token) { send({ op: "set_ha", url: url, token: token, tag: nextTag() }) }
  function setExpose(key, on) { send({ op: "expose", key: key, on: on }) }
  function runPc(key, value) { send({ op: "run_pc", key: key, value: value === undefined ? null : value, tag: nextTag() }) }
  function setDeviceName(name) { send({ op: "set_device", name: name, tag: nextTag() }) }
  function regenerateKey() { send({ op: "regen_key" }) }
  function addCommand(name, command) { send({ op: "add_command", name: name, command: command, tag: nextTag() }) }
  function removeCommand(id) { send({ op: "remove_command", id: id }) }
  function refresh() { send({ op: "refresh" }) }

  function isFavorite(entityId) { return favoriteIds.indexOf(entityId) >= 0 }

  function pcState(key) { return pcStates[key] }

  function replaceFavorite(entity) {
    var next = favorites.slice()
    for (var i = 0; i < next.length; i++) {
      if (next[i].entity_id === entity.entity_id) {
        next[i] = entity
        favorites = next
        return
      }
    }
  }

  function handle(line) {
    var m
    try { m = JSON.parse(line) } catch (e) { return }
    if (!m || typeof m.ev !== "string") return
    switch (m.ev) {
    case "daemon":
      daemonRunning = m.running === true
      if (!daemonRunning) {
        favorites = []
        pcEntities = []
      }
      break
    case "status":
      if (m.ha) ha = m.ha
      if (m.agent) agent = m.agent
      break
    case "favorites":
      favorites = Array.isArray(m.entities) ? m.entities : []
      break
    case "state":
      if (m.entity) replaceFavorite(m.entity)
      break
    case "pc":
      pcEntities = Array.isArray(m.entities) ? m.entities : []
      pcStates = m.states || {}
      pcRevision += 1
      break
    case "pc_states":
      var next = Object.assign({}, pcStates)
      for (var k in m.states) next[k] = m.states[k]
      pcStates = next
      pcRevision += 1
      break
    case "config":
      customCommands = Array.isArray(m.custom_commands) ? m.custom_commands : []
      favoriteIds = Array.isArray(m.favorites) ? m.favorites : []
      break
    case "list":
      listEntities = Array.isArray(m.entities) ? m.entities : []
      listDomains = m.domains || {}
      if (Array.isArray(m.favorites)) favoriteIds = m.favorites
      listLoading = false
      break
    case "result":
      if (!m.ok && m.error) lastError = String(m.error)
      else if (m.ok) lastError = ""
      resultArrived(String(m.tag || ""), m.ok === true, String(m.error || ""))
      break
    }
  }

  property Process attach: Process {
    command: [root.executable, "attach"]
    stdinEnabled: true
    running: true
    stdout: SplitParser {
      splitMarker: "\n"
      onRead: function(line) { root.handle(line) }
    }
    onStarted: root.attachFailed = false
    onExited: function(code) {
      root.daemonRunning = false
      root.attachFailed = true
      root.restartTimer.start()
    }
  }

  // `attach` only exits if the binary is missing or crashed; retry slowly.
  property Timer restartTimer: Timer {
    interval: 5000
    onTriggered: root.attach.running = true
  }
}
