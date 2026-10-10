import AppKit

// A native guest window verifies actual mouse buttons without UI automation
// permissions. The VM is disposable; no host settings or applications change.
final class ClickView: NSView {
    override var acceptsFirstResponder: Bool { true }
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func draw(_ dirtyRect: NSRect) {
        NSColor.windowBackgroundColor.setFill()
        bounds.fill()
        let label = "VNC mouse integration: left, right, left"
        (label as NSString).draw(at: NSPoint(x: 40, y: 40), withAttributes: [
            .font: NSFont.systemFont(ofSize: 24),
            .foregroundColor: NSColor.labelColor,
        ])
    }

    private func record(_ event: String) {
        do {
            let file = try FileHandle(forWritingTo: URL(fileURLWithPath: "/tmp/vnc-commands-mouse-marker"))
            defer { try? file.close() }
            try file.seekToEnd()
            try file.write(contentsOf: Data((event + "\n").utf8))
        } catch {
            fputs("Mouse marker write failed: \(error)\n", stderr)
            NSApplication.shared.terminate(nil)
        }
    }

    override func mouseDown(with event: NSEvent) { record("left-down") }
    override func mouseUp(with event: NSEvent) { record("left-up") }
    override func rightMouseDown(with event: NSEvent) { record("right-down") }
    override func rightMouseUp(with event: NSEvent) { record("right-up") }
}

let app = NSApplication.shared
app.setActivationPolicy(.regular)
guard let screen = NSScreen.main else { fatalError("No graphical screen") }
let window = NSWindow(contentRect: screen.frame, styleMask: .borderless, backing: .buffered, defer: false)
window.contentView = ClickView(frame: NSRect(origin: .zero, size: screen.frame.size))
window.isReleasedWhenClosed = false
window.makeKeyAndOrderFront(nil)
app.activate(ignoringOtherApps: true)
DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
    do {
        try Data(String(ProcessInfo.processInfo.processIdentifier).utf8).write(to: URL(fileURLWithPath: "/tmp/vnc-commands-mouse-ready"))
    } catch {
        fputs("Mouse ready marker write failed: \(error)\n", stderr)
        app.terminate(nil)
    }
}
app.run()
