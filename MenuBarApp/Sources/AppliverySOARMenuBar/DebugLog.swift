import Foundation

/// TEMPORARY: a plain-file diagnostic channel, used only for the
/// still-unresolved panel positioning investigation
/// (github.com/raulcnadal/applivery-soar-agent-macos). NSLog output for
/// this app's own calls has proven completely unreliable to capture via
/// `log stream`/`log show` on the affected test machine — a virtualized
/// Mac ("VirtualMac2,1") — across two full rounds of testing: OS framework
/// log lines from the exact same process (AppKit, Foundation, etc.) show up
/// perfectly normally in the unified log around the very same click, but
/// this app's own NSLog calls never appear at all, even ones placed
/// unconditionally at the very top of the button's action method with no
/// guard clauses in front of them. The binary was independently confirmed
/// (via `strings` + file mtime) to actually contain that logging code, so
/// this isn't a stale-build issue — something about NSLog delivery itself
/// is unreliable in this environment specifically.
///
/// Writing straight to a plain file sidesteps the unified logging pipeline
/// entirely, so it can't be affected by whatever is swallowing NSLog output
/// here. `/tmp` needs no special permissions for the console user, and this
/// app isn't sandboxed (see Info.plist's own doc comment), so a plain
/// `FileHandle` append is all this needs. Safe to delete once the
/// underlying positioning bug is confirmed fixed.
enum DebugLog {
    private static let path = "/tmp/applivery-soar-panel-debug.log"
    private static let formatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd HH:mm:ss.SSS"
        return f
    }()

    static func write(_ message: String) {
        let line = "\(formatter.string(from: Date())) \(message)\n"
        guard let data = line.data(using: .utf8) else { return }

        if !FileManager.default.fileExists(atPath: path) {
            FileManager.default.createFile(atPath: path, contents: nil)
        }
        guard let handle = FileHandle(forWritingAtPath: path) else { return }
        defer { try? handle.close() }
        handle.seekToEndOfFile()
        handle.write(data)
    }
}
