import QtQuick
import Quickshell.Io
import qs.Commons
import qs.Commons as Commons
import qs.Ui
import "Model.js" as Model

// Everything inside the popup. Kept separate from Panel.qml (bar button and
// popup window) so it can be rendered on its own for previews and tests.
//
// Four views:
//   Home      favorites from Home Assistant, with inline controls
//   Devices   search every entity and star favorites
//   This PC   what this machine exposes to Home Assistant, live values
//   Settings  Home Assistant connection and device name
PanelKeyCatcher {
  id: root

  property QtObject bar: null
  property var svc: null
  property bool opened: true

  readonly property bool ready: svc !== null && svc !== undefined
  readonly property bool daemonUp: ready && svc.daemonRunning
  readonly property string haPhase: ready ? svc.ha.phase : "idle"

  readonly property var views: [
    { id: "home", label: "Home", icon: Model.HOME },
    { id: "devices", label: "Devices", icon: Model.DEVICES },
    { id: "pc", label: "This PC", icon: Model.LAPTOP },
    { id: "settings", label: "Settings", icon: Model.COG }
  ]
  property string view: "home"
  property string query: ""
  property string domainFilter: "all"
  property bool editing: false

  readonly property color fg: bar ? bar.foreground : Commons.Color.foreground
  readonly property string font: bar ? bar.fontFamily : Style.font.family

  implicitHeight: column.implicitHeight

  function setView(id) {
    view = id
    editing = false
    if (id === "devices" && ready) svc.list(query, domainFilter)
  }

  function switchView(delta) {
    var i = 0
    for (var k = 0; k < views.length; k++) if (views[k].id === view) i = k
    setView(views[(i + delta + views.length) % views.length].id)
  }

  function act(entity) {
    if (!ready || Model.unavailable(entity)) return
    var c = Model.toggleCall(entity)
    svc.call(c[0], c[1], entity.entity_id, c[2])
  }

  onOpenedChanged: {
    if (opened && ready) {
      svc.refresh()
      if (view === "devices") svc.list(query, domainFilter)
    }
    if (!opened) editing = false
  }

  Timer {
    id: searchDebounce
    interval: 150
    onTriggered: if (root.ready) root.svc.list(root.query, root.domainFilter)
  }

  Process { id: copyProc }

  blocked: root.editing
  onMoveRequested: function(dx, dy) { if (dx !== 0) root.switchView(dx) }
  onTextKey: function(t, modifiers) {
    if (t === "r" && root.ready) root.svc.refresh()
    else if (t === "1") root.setView("home")
    else if (t === "2") root.setView("devices")
    else if (t === "3") root.setView("pc")
    else if (t === "4") root.setView("settings")
  }

  Column {
    id: column
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    spacing: Style.space(12)

    // ---------- Header ----------
    Item {
      width: parent.width
      implicitHeight: Math.max(heroIcon.implicitHeight, heroLabels.implicitHeight)

      Label {
        id: heroIcon
        text: Model.BRAND
        font.pixelSize: Style.font.display
        anchors.left: parent.left
        anchors.verticalCenter: parent.verticalCenter
      }

      Column {
        id: heroLabels
        anchors.left: heroIcon.right
        anchors.leftMargin: Style.space(14)
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        spacing: Style.space(2)

        Label {
          width: parent.width
          text: "Home Assistant"
          font.pixelSize: Style.font.title
          font.bold: true
          elide: Text.ElideRight
        }
        Label {
          width: parent.width
          text: Model.phaseText(root.daemonUp, root.ready ? root.svc.ha : null).toUpperCase()
          color: root.ready && root.haPhase === "error" ? Commons.Color.accent : Qt.darker(root.fg, 1.4)
          font.pixelSize: Style.font.caption
          font.bold: true
          font.letterSpacing: 1.2
          elide: Text.ElideRight
        }
      }
    }

    // ---------- View tabs ----------
    Row {
      id: tabs
      width: parent.width
      spacing: Style.space(6)
      readonly property real cell: (width - spacing * (root.views.length - 1)) / root.views.length

      Repeater {
        model: root.views
        Button {
          required property var modelData
          width: tabs.cell
          iconText: modelData.icon
          iconSize: Style.font.body
          text: modelData.label
          fontSize: Style.font.bodySmall
          foreground: root.fg
          fontFamily: root.font
          bordered: true
          active: root.view === modelData.id
          onClicked: root.setView(modelData.id)
        }
      }
    }

    PanelSeparator { foreground: root.fg }

    // ---------- Daemon missing ----------
    Column {
      visible: !root.daemonUp
      width: parent.width
      spacing: Style.space(8)
      Label { text: "omahassd isn't running"; font.bold: true }
      Dim {
        width: parent.width
        wrapMode: Text.Wrap
        text: root.ready && root.svc.attachFailed
          ? "Couldn't start `omahassd attach`. Is omahassd installed in your PATH (e.g. ~/.local/bin)?"
          : "Start it with: systemctl --user enable --now omahassd"
      }
    }

    Loader {
      width: parent.width
      active: root.daemonUp
      visible: active
      sourceComponent: root.view === "home" ? homeView
        : root.view === "devices" ? devicesView
        : root.view === "pc" ? pcView : settingsView
    }
  }

  // ================= Home =================
  Component {
    id: homeView
    Column {
      spacing: Style.space(8)

      Column {
        visible: root.haPhase !== "connected" && root.svc.favorites.length === 0
        width: parent.width
        spacing: Style.space(8)
        Dim {
          width: parent.width
          wrapMode: Text.Wrap
          text: root.haPhase === "unconfigured"
            ? "Connect this panel to Home Assistant to control your devices."
            : root.haPhase === "connecting" ? "Connecting to Home Assistant…" : (root.svc.ha.error || "Not connected.")
        }
        Button {
          visible: root.haPhase === "unconfigured" || root.haPhase === "error"
          text: "Open settings"
          iconText: Model.COG
          foreground: root.fg
          fontFamily: root.font
          bordered: true
          onClicked: root.setView("settings")
        }
      }

      Dim {
        visible: root.haPhase === "connected" && root.svc.favorites.length === 0
        width: parent.width
        wrapMode: Text.Wrap
        text: "No favorites yet. Star devices in Devices to pin them here."
      }

      Flickable {
        width: parent.width
        height: Math.min(contentHeight, Style.space(460))
        contentHeight: favColumn.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height

        Column {
          id: favColumn
          width: parent.width
          spacing: Style.space(4)
          Repeater {
            model: root.svc.favorites
            EntityRow {
              required property var modelData
              entity: modelData
              width: favColumn.width
            }
          }
        }
      }

      Dim {
        visible: root.svc.lastError !== ""
        width: parent.width
        wrapMode: Text.Wrap
        color: Commons.Color.accent
        text: root.svc.lastError
      }
    }
  }

  // ================= Devices =================
  Component {
    id: devicesView
    Column {
      spacing: Style.space(10)

      TextField {
        id: search
        width: parent.width
        placeholderText: "Search devices…"
        text: root.query
        foreground: root.fg
        font.family: root.font
        onTextChanged: { root.query = text; searchDebounce.restart() }
        onActiveFocusChanged: root.editing = activeFocus
        Keys.onEscapePressed: function(event) { focus = false; root.editing = false; event.accepted = true }
      }

      Flow {
        width: parent.width
        spacing: Style.space(4)
        Repeater {
          model: Model.domainChips(root.svc.listDomains)
          Button {
            required property var modelData
            text: Model.domainLabel(modelData)
            fontSize: Style.font.caption
            foreground: root.fg
            fontFamily: root.font
            horizontalPadding: Style.space(8)
            verticalPadding: Style.space(3)
            bordered: true
            active: root.domainFilter === modelData
            onClicked: { root.domainFilter = modelData; root.svc.list(root.query, modelData) }
          }
        }
      }

      Dim {
        visible: root.haPhase !== "connected"
        text: "Not connected to Home Assistant."
      }
      Dim {
        visible: root.haPhase === "connected" && !root.svc.listLoading && root.svc.listEntities.length === 0
        text: "Nothing matches."
      }

      ListView {
        id: list
        width: parent.width
        height: Math.min(contentHeight, Style.space(400))
        clip: true
        spacing: Style.space(2)
        boundsBehavior: Flickable.StopAtBounds
        model: root.svc.listEntities
        delegate: Item {
          required property var modelData
          width: list.width
          implicitHeight: Style.space(40)
          height: implicitHeight

          Label {
            id: dIcon
            anchors.left: parent.left
            anchors.verticalCenter: parent.verticalCenter
            width: Style.space(26)
            text: Model.icon(modelData)
            font.pixelSize: Style.font.title
            opacity: Model.unavailable(modelData) ? 0.4 : 1
          }
          Column {
            anchors.left: dIcon.right
            anchors.leftMargin: Style.space(8)
            anchors.right: star.left
            anchors.rightMargin: Style.space(8)
            anchors.verticalCenter: parent.verticalCenter
            Label { width: parent.width; text: modelData.name; elide: Text.ElideRight; font.pixelSize: Style.font.body }
            Dim {
              width: parent.width
              text: modelData.entity_id + (Model.stateText(modelData) ? "  ·  " + Model.stateText(modelData) : "")
              elide: Text.ElideRight
              font.pixelSize: Style.font.caption
            }
          }
          Button {
            id: star
            anchors.right: parent.right
            anchors.verticalCenter: parent.verticalCenter
            readonly property bool fav: root.svc.favoriteIds.indexOf(modelData.entity_id) >= 0
            iconText: fav ? Model.STAR : Model.STAR_OUTLINE
            iconSize: Style.font.title
            foreground: root.fg
            fontFamily: root.font
            active: fav
            tooltipText: fav ? "Remove from Home" : "Add to Home"
            onClicked: root.svc.setFavorite(modelData.entity_id, !fav)
          }
        }
      }
    }
  }

  // ================= This PC =================
  Component {
    id: pcView
    Column {
      spacing: Style.space(10)

      // Pairing card
      Rectangle {
        width: parent.width
        implicitHeight: pairCol.implicitHeight + Style.space(20)
        radius: Style.cornerRadius
        color: Qt.rgba(root.fg.r, root.fg.g, root.fg.b, 0.06)

        Column {
          id: pairCol
          anchors.fill: parent
          anchors.margins: Style.space(10)
          spacing: Style.space(6)

          Row {
            spacing: Style.space(8)
            Label { text: Model.LAPTOP; font.pixelSize: Style.font.title }
            Label { text: root.svc.agent.device_name; font.bold: true; font.pixelSize: Style.font.body }
            Dim {
              text: root.svc.agent.clients && root.svc.agent.clients.length > 0
                ? "· paired with Home Assistant" : "· waiting for Home Assistant"
              font.pixelSize: Style.font.bodySmall
            }
          }
          Row {
            spacing: Style.space(8)
            Dim { text: "Pairing key"; anchors.verticalCenter: parent.verticalCenter; font.pixelSize: Style.font.bodySmall }
            Label {
              text: root.svc.agent.key
              font.pixelSize: Style.font.title
              font.bold: true
              font.letterSpacing: 2
              anchors.verticalCenter: parent.verticalCenter
            }
            Button {
              iconText: Model.COPY
              tooltipText: "Copy"
              foreground: root.fg
              fontFamily: root.font
              onClicked: { copyProc.command = ["wl-copy", root.svc.agent.key]; copyProc.running = true }
            }
            Button {
              iconText: Model.REFRESH
              tooltipText: "New key (Home Assistant must re-pair)"
              foreground: root.fg
              fontFamily: root.font
              onClicked: root.svc.regenerateKey()
            }
          }
          Dim {
            width: parent.width
            wrapMode: Text.Wrap
            font.pixelSize: Style.font.caption
            text: "Home Assistant discovers this PC automatically (" + root.svc.agent.ip + ":" + root.svc.agent.port + "). Accept it under Settings → Devices & services and enter the key."
          }
        }
      }

      Dim {
        width: parent.width
        wrapMode: Text.Wrap
        font.pixelSize: Style.font.caption
        text: "Switch on what Home Assistant should see. Changes apply instantly."
      }

      Flickable {
        width: parent.width
        height: Math.min(contentHeight, Style.space(380))
        contentHeight: groups.implicitHeight
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        interactive: contentHeight > height

        Column {
          id: groups
          width: parent.width
          spacing: Style.space(10)

          Repeater {
            model: Model.groupPc(root.svc.pcEntities)
            Column {
              required property var modelData
              width: groups.width
              spacing: Style.space(2)

              PanelSectionHeader {
                text: modelData.name
                foreground: root.fg
                fontFamily: root.font
              }

              Repeater {
                model: modelData.entities
                Item {
                  required property var modelData
                  width: groups.width
                  implicitHeight: Style.space(34)

                  Label {
                    id: pIcon
                    anchors.left: parent.left
                    anchors.verticalCenter: parent.verticalCenter
                    width: Style.space(24)
                    text: Model.pcIcon(modelData)
                    font.pixelSize: Style.font.body
                    opacity: modelData.enabled ? 1 : 0.45
                  }
                  Label {
                    id: pName
                    anchors.left: pIcon.right
                    anchors.leftMargin: Style.space(6)
                    anchors.verticalCenter: parent.verticalCenter
                    width: Math.min(implicitWidth, parent.width * 0.42)
                    text: modelData.name
                    elide: Text.ElideRight
                    font.pixelSize: Style.font.bodySmall
                    opacity: modelData.enabled ? 1 : 0.6
                  }
                  Dim {
                    anchors.left: pName.right
                    anchors.leftMargin: Style.space(8)
                    anchors.right: pSwitch.left
                    anchors.rightMargin: Style.space(8)
                    anchors.verticalCenter: parent.verticalCenter
                    horizontalAlignment: Text.AlignRight
                    elide: Text.ElideRight
                    font.pixelSize: Style.font.bodySmall
                    // pcRevision makes this re-evaluate on every state push.
                    text: root.svc.pcRevision >= 0 ? Model.pcValue(modelData, root.svc.pcStates[modelData.key]) : ""
                  }
                  ToggleSwitch {
                    id: pSwitch
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    checked: modelData.enabled
                    foreground: root.fg
                    cursorRing: false
                    onToggled: root.svc.setExpose(modelData.key, !modelData.enabled)
                  }
                }
              }
            }
          }

          // Custom commands
          Column {
            width: groups.width
            spacing: Style.space(6)

            PanelSectionHeader { text: "Custom commands"; foreground: root.fg; fontFamily: root.font }

            Repeater {
              model: root.svc.customCommands
              Item {
                required property var modelData
                width: groups.width
                implicitHeight: Style.space(36)
                Column {
                  anchors.left: parent.left
                  anchors.right: cmdButtons.left
                  anchors.rightMargin: Style.space(8)
                  anchors.verticalCenter: parent.verticalCenter
                  Label { width: parent.width; text: modelData.name; elide: Text.ElideRight; font.pixelSize: Style.font.bodySmall }
                  Dim { width: parent.width; text: modelData.command; elide: Text.ElideRight; font.pixelSize: Style.font.caption; font.family: "monospace" }
                }
                Row {
                  id: cmdButtons
                  anchors.right: parent.right
                  anchors.verticalCenter: parent.verticalCenter
                  spacing: Style.space(4)
                  Button {
                    iconText: Model.RUN
                    tooltipText: "Run now"
                    foreground: root.fg
                    fontFamily: root.font
                    onClicked: root.svc.runPc("cmd_" + modelData.id)
                  }
                  Button {
                    iconText: Model.DELETE
                    tooltipText: "Remove"
                    foreground: root.fg
                    fontFamily: root.font
                    onClicked: root.svc.removeCommand(modelData.id)
                  }
                }
              }
            }

            Row {
              width: groups.width
              spacing: Style.space(6)
              TextField {
                id: cmdName
                width: Style.space(110)
                placeholderText: "Name"
                foreground: root.fg
                font.family: root.font
                onActiveFocusChanged: root.editing = activeFocus || cmdLine.activeFocus
              }
              TextField {
                id: cmdLine
                width: parent.width - cmdName.width - addCmd.width - parent.spacing * 2
                placeholderText: "Shell command, e.g. omarchy-launch-browser"
                foreground: root.fg
                font.family: root.font
                onActiveFocusChanged: root.editing = activeFocus || cmdName.activeFocus
                onAccepted: addCmd.clicked()
              }
              Button {
                id: addCmd
                iconText: Model.PLUS
                tooltipText: "Add as a Home Assistant button"
                foreground: root.fg
                fontFamily: root.font
                bordered: true
                onClicked: {
                  if (cmdName.text.trim() === "" || cmdLine.text.trim() === "") return
                  root.svc.addCommand(cmdName.text.trim(), cmdLine.text.trim())
                  cmdName.text = ""
                  cmdLine.text = ""
                }
              }
            }
          }
        }
      }
    }
  }

  // ================= Settings =================
  Component {
    id: settingsView
    Column {
      spacing: Style.space(10)

      PanelSectionHeader { text: "Home Assistant"; foreground: root.fg; fontFamily: root.font }

      TextField {
        id: urlField
        width: parent.width
        placeholderText: "https://homeassistant.local:8123"
        text: root.svc.ha.url
        foreground: root.fg
        font.family: root.font
        onActiveFocusChanged: root.editing = activeFocus || tokenField.activeFocus
      }
      TextField {
        id: tokenField
        width: parent.width
        password: true
        placeholderText: root.svc.ha.has_token ? "Token saved. Paste a new one to replace it." : "Long-lived access token (Profile → Security)"
        foreground: root.fg
        font.family: root.font
        onActiveFocusChanged: root.editing = activeFocus || urlField.activeFocus
        onAccepted: connectButton.clicked()
      }
      Row {
        spacing: Style.space(8)
        Button {
          id: connectButton
          text: "Connect"
          iconText: Model.BRAND
          foreground: root.fg
          fontFamily: root.font
          bordered: true
          onClicked: {
            root.svc.setHa(urlField.text.trim(), tokenField.text.trim())
            tokenField.text = ""
          }
        }
        Dim {
          anchors.verticalCenter: parent.verticalCenter
          text: Model.phaseText(true, root.svc.ha)
          color: root.haPhase === "error" ? Commons.Color.accent : Qt.darker(root.fg, 1.4)
          font.pixelSize: Style.font.bodySmall
        }
      }

      PanelSeparator { foreground: root.fg }
      PanelSectionHeader { text: "This PC"; foreground: root.fg; fontFamily: root.font }

      Row {
        width: parent.width
        spacing: Style.space(8)
        TextField {
          id: nameField
          width: parent.width - saveName.width - parent.spacing
          placeholderText: "Device name in Home Assistant"
          text: root.svc.agent.device_name
          foreground: root.fg
          font.family: root.font
          onActiveFocusChanged: root.editing = activeFocus
          onAccepted: saveName.clicked()
        }
        Button {
          id: saveName
          text: "Rename"
          foreground: root.fg
          fontFamily: root.font
          bordered: true
          onClicked: root.svc.setDeviceName(nameField.text.trim())
        }
      }
      Dim {
        width: parent.width
        wrapMode: Text.Wrap
        font.pixelSize: Style.font.caption
        text: "Agent listening on " + root.svc.agent.ip + ":" + root.svc.agent.port
          + ". Config: ~/.config/omahass/config.json"
      }
      Dim {
        visible: root.svc.lastError !== ""
        width: parent.width
        wrapMode: Text.Wrap
        color: Commons.Color.accent
        text: root.svc.lastError
      }
    }
  }

  // ================= Pieces =================

  component Label: Text {
    textFormat: Text.PlainText
    color: root.fg
    font.family: root.font
    font.pixelSize: Style.font.body
  }

  component Dim: Text {
    textFormat: Text.PlainText
    color: Qt.darker(root.fg, 1.4)
    font.family: root.font
    font.pixelSize: Style.font.bodySmall
  }

  // One favorite: icon, name, state and the control that fits its domain.
  component EntityRow: Column {
    id: row
    property var entity: ({})
    readonly property string kind: Model.kind(entity)
    readonly property bool off: Model.unavailable(entity)
    spacing: Style.space(4)

    Item {
      width: row.width
      implicitHeight: Style.space(44)

      Label {
        id: eIcon
        anchors.left: parent.left
        anchors.verticalCenter: parent.verticalCenter
        width: Style.space(28)
        text: Model.icon(row.entity)
        font.pixelSize: Style.font.title + 2
        color: Model.isOn(row.entity) ? Commons.Color.accent : root.fg
        opacity: row.off ? 0.4 : 1
      }
      Column {
        anchors.left: eIcon.right
        anchors.leftMargin: Style.space(8)
        anchors.right: trailing.left
        anchors.rightMargin: Style.space(8)
        anchors.verticalCenter: parent.verticalCenter
        Label { width: parent.width; text: row.entity.name || row.entity.entity_id; elide: Text.ElideRight; opacity: row.off ? 0.6 : 1 }
        Dim {
          width: parent.width
          visible: text !== "" && row.kind !== "readonly"
          text: Model.stateText(row.entity)
          elide: Text.ElideRight
          font.pixelSize: Style.font.caption
        }
      }

      Item {
        id: trailing
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        width: trailingLoader.item ? trailingLoader.item.implicitWidth : 0
        height: trailingLoader.item ? trailingLoader.item.implicitHeight : 0
        Loader {
          id: trailingLoader
          anchors.right: parent.right
          anchors.verticalCenter: parent.verticalCenter
          sourceComponent: row.kind === "toggle" || row.kind === "lock" ? toggleControl
            : row.kind === "run" || row.kind === "press" ? runControl
            : row.kind === "media" ? mediaControl
            : row.kind === "cover" ? coverControl
            : row.kind === "climate" ? climateControl
            : readonlyControl
        }
      }
    }

    PanelSlider {
      visible: Model.dimmable(row.entity) && row.entity.state === "on"
      width: row.width - Style.space(36)
      x: Style.space(36)
      bar: root.bar
      minimum: 1
      maximum: 100
      step: 1
      integer: true
      value: Model.brightnessPct(row.entity)
      onReleased: function(v) { root.svc.call("light", "turn_on", row.entity.entity_id, { brightness_pct: Math.round(v) }) }
    }

    Component {
      id: toggleControl
      ToggleSwitch {
        checked: Model.isOn(row.entity)
        interactive: !row.off
        foreground: root.fg
        cursorRing: false
        onToggled: root.act(row.entity)
      }
    }
    Component {
      id: runControl
      Button {
        text: row.kind === "run" ? "Run" : "Press"
        fontSize: Style.font.bodySmall
        foreground: root.fg
        fontFamily: root.font
        bordered: true
        onClicked: root.act(row.entity)
      }
    }
    Component {
      id: mediaControl
      Row {
        spacing: Style.space(2)
        Button { iconText: Model.PREV; foreground: root.fg; fontFamily: root.font; onClicked: root.svc.call("media_player", "media_previous_track", row.entity.entity_id, {}) }
        Button {
          iconText: row.entity.state === "playing" ? Model.PAUSE : Model.PLAY
          foreground: root.fg
          fontFamily: root.font
          onClicked: root.svc.call("media_player", "media_play_pause", row.entity.entity_id, {})
        }
        Button { iconText: Model.NEXT; foreground: root.fg; fontFamily: root.font; onClicked: root.svc.call("media_player", "media_next_track", row.entity.entity_id, {}) }
      }
    }
    Component {
      id: coverControl
      Row {
        spacing: Style.space(2)
        Button { iconText: Model.UP; foreground: root.fg; fontFamily: root.font; onClicked: root.svc.call("cover", "open_cover", row.entity.entity_id, {}) }
        Button { iconText: Model.STOP; foreground: root.fg; fontFamily: root.font; onClicked: root.svc.call("cover", "stop_cover", row.entity.entity_id, {}) }
        Button { iconText: Model.DOWN; foreground: root.fg; fontFamily: root.font; onClicked: root.svc.call("cover", "close_cover", row.entity.entity_id, {}) }
      }
    }
    Component {
      id: climateControl
      Row {
        spacing: Style.space(2)
        readonly property real target: Number(Model.attr(row.entity, "temperature"))
        readonly property real stepSize: Number(Model.attr(row.entity, "target_temp_step")) || 0.5
        Button {
          iconText: Model.MINUS
          foreground: root.fg
          fontFamily: root.font
          enabled: isFinite(parent.target)
          onClicked: root.svc.call("climate", "set_temperature", row.entity.entity_id, { temperature: parent.target - parent.stepSize })
        }
        Button {
          iconText: Model.PLUS
          foreground: root.fg
          fontFamily: root.font
          enabled: isFinite(parent.target)
          onClicked: root.svc.call("climate", "set_temperature", row.entity.entity_id, { temperature: parent.target + parent.stepSize })
        }
      }
    }
    Component {
      id: readonlyControl
      Label {
        text: Model.stateText(row.entity)
        font.pixelSize: Style.font.body
        opacity: row.off ? 0.5 : 1
      }
    }
  }
}
