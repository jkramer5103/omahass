import QtQuick
import qs.Commons
import qs.Ui
import "." as Local
import "Model.js" as Model

// Bar button plus popup window. The popup's contents are PanelContent.qml.
Panel {
  id: root
  moduleName: "omahass"
  ipcTarget: "omahass"
  manageIpc: false

  readonly property var svc: bar && bar.shell ? bar.shell.serviceFor("omahass") : null
  property string pendingView: ""

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  ShellIpc {
    target: "omahass"
    function open(): void { root.open() }
    function close(): void { root.close() }
    function toggle(): void { root.toggle() }
    function show(view: string): void {
      if (panel.loadedContent) panel.loadedContent.setView(view)
      else root.pendingView = view
      root.open()
    }
    function status(): string {
      if (!root.svc) return "no service"
      return JSON.stringify({ daemon: root.svc.daemonRunning, ha: root.svc.ha.phase, error: root.svc.ha.error, favorites: root.svc.favorites.length, pcEntities: root.svc.pcEntities.length, agentClients: root.svc.agent.clients })
    }
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: Model.BRAND
    // Urgent colour when something needs attention.
    active: !!root.svc && (!root.svc.daemonRunning || root.svc.ha.phase === "error")
    tooltipText: ""
    onPressed: function(b) { root.toggle() }
  }

  KeyboardPanel {
    id: panel
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: panel.loadedContent
    contentWidth: panel.fittedContentWidth(Style.space(430))
    contentHeight: panel.fittedContentHeight(panel.loadedContentHeight)

    content: Local.PanelContent {
      anchors.fill: parent
      bar: root.bar
      svc: root.svc
      opened: root.opened
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      Component.onCompleted: if (root.pendingView !== "") { setView(root.pendingView); root.pendingView = "" }
    }
  }
}
