import AppKit

// Plain main.swift entry point rather than a SwiftUI `@main struct: App` —
// this app deliberately builds its own NSStatusItem/StatusPanel (see
// AppDelegate.swift's doc comment for why: broader macOS version support
// than SwiftUI's MenuBarExtra, and closer parity with the Windows tray's own
// hand-built status-item approach) rather than using SwiftUI's app
// lifecycle, so the classic AppKit NSApplicationMain-equivalent bootstrap is
// the natural fit.
let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
