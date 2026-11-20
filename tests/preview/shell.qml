// Renders the panel contents off-screen against a running omahassd and
// saves one PNG per view. Driven by tests/preview/run.sh.
import QtQuick
import Quickshell
import qs.Commons
import qs.Commons as Commons
import "omahass" as P

ShellRoot {
  id: shellRoot
  readonly property string outDir: Quickshell.env("PREVIEW_OUT")
  readonly property var shots: ["home", "devices", "pc", "settings"]
  property int shot: -1

  P.Service { id: svc }

  FloatingWindow {
    id: win
    implicitWidth: 480
    implicitHeight: 1100
    color: "transparent"

    QtObject {
      id: fakeBar
      property color foreground: Commons.Color.foreground
      property color background: Commons.Color.background
      property color barForeground: Commons.Color.foreground
      property string fontFamily: Style.font.family
      property string position: "top"
      property bool vertical: false
    }

    Rectangle {
      id: card
      width: 470
      height: content.implicitHeight + 2 * Style.spacing.popupPadding + 4
      color: Commons.Color.background
      border.color: Qt.rgba(Commons.Color.foreground.r, Commons.Color.foreground.g, Commons.Color.foreground.b, 0.35)
      border.width: 2

      P.PanelContent {
        id: content
        x: Style.spacing.popupPadding + 2
        y: Style.spacing.popupPadding + 2
        width: card.width - 2 * x
        bar: fakeBar
        svc: svc
      }
    }
  }

  Timer {
    id: ticker
    interval: 1500
    repeat: true
    running: true
    onTriggered: {
      if (shellRoot.shot < 0) {
        // Wait for the daemon and fake HA, then pick favorites view first.
        if (!(svc.daemonRunning && svc.ha.phase === "connected" && svc.favorites.length > 0)) return
        shellRoot.shot = 0
        content.setView(shellRoot.shots[0])
        return
      }
      // Grab first, switch view only once the frame is saved.
      ticker.stop()
      var name = shellRoot.shots[shellRoot.shot]
      card.grabToImage(function(result) {
        result.saveToFile(shellRoot.outDir + "/" + name + ".png")
        shellRoot.shot += 1
        if (shellRoot.shot >= shellRoot.shots.length) { quitTimer.start(); return }
        content.setView(shellRoot.shots[shellRoot.shot])
        ticker.start()
      })
    }
  }
  Timer { id: quitTimer; interval: 800; onTriggered: Qt.quit() }
  Timer { interval: 30000; running: true; onTriggered: { console.log("preview: timed out"); Qt.quit() } }
}
