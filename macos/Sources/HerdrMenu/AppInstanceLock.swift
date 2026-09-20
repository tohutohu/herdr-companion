import AppKit
import Darwin
import Foundation

/// An advisory per-user lock that prevents two copies of the menu-bar manager.
final class AppInstanceLock {
    static let shared = AppInstanceLock()

    private var descriptor: Int32 = -1

    private init() {}

    func acquire() -> Bool {
        guard descriptor == -1 else { return true }
        let directory = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Herdr Companion", isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        } catch {
            return false
        }
        let path = directory.appendingPathComponent("instance.lock").path
        let fd = Darwin.open(path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard fd >= 0 else { return false }
        guard Darwin.lockf(fd, F_TLOCK, 0) == 0 else {
            Darwin.close(fd)
            return false
        }
        descriptor = fd
        return true
    }

    func activateExisting() {
        let apps = NSRunningApplication.runningApplications(
            withBundleIdentifier: "com.tohutohu.herdrcompanion.mac"
        )
        apps.first(where: { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier })?.activate(
            options: [.activateAllWindows, .activateIgnoringOtherApps]
        )
    }

    deinit {
        guard descriptor >= 0 else { return }
        Darwin.lockf(descriptor, F_ULOCK, 0)
        Darwin.close(descriptor)
    }
}
