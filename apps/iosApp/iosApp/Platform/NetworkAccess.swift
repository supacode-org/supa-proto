import CoreTelephony
import Foundation
import SwiftUI
import UIKit

/// On iPhones sold in mainland China, iOS asks once per app whether it may use
/// WLAN and cellular data ("Wireless Data"). The prompt only appears when the
/// app itself first reaches the network through the system networking stack;
/// the packet tunnel extension and the Go core's sockets do not trigger it, and
/// until it is answered traffic from the app and its extension is blocked.
///
/// `prime()` makes a small request on launch so the prompt appears right after
/// installation, and repeats it on later launches and foregrounds until one
/// succeeds. When the request fails while iOS reports data as restricted for
/// the app, `isRestricted` turns true so the UI can point to Settings.
@MainActor
final class NetworkAccess: ObservableObject {
    @Published var isRestricted = false

    private static let primedKey = "networkAccessPrimed"
    /// Apple's own connectivity check page; the response body is ignored.
    private static let probeURL = URL(string: "https://www.apple.com/library/test/success.html")!

    private let cellularData = CTCellularData()
    private var restriction: CTCellularDataRestrictedState = .restrictedStateUnknown
    private var probing = false
    private var lastProbeFailed = false

    init() {
        cellularData.cellularDataRestrictionDidUpdateNotifier = { @Sendable [weak self] state in
            Task { @MainActor in self?.update(state) }
        }
    }

    private var primed: Bool {
        get { UserDefaults.standard.bool(forKey: Self.primedKey) }
        set { UserDefaults.standard.set(newValue, forKey: Self.primedKey) }
    }

    func prime() async {
        guard !primed, !probing else { return }
        probing = true
        defer { probing = false }
        var request = URLRequest(url: Self.probeURL, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 10)
        request.httpMethod = "HEAD"
        let session = URLSession(configuration: .ephemeral)
        defer { session.finishTasksAndInvalidate() }
        do {
            _ = try await session.data(for: request)
            primed = true
            lastProbeFailed = false
            isRestricted = false
        } catch {
            lastProbeFailed = true
            isRestricted = restriction == .restricted
        }
    }

    private func update(_ state: CTCellularDataRestrictedState) {
        restriction = state
        if state == .notRestricted {
            isRestricted = false
            // The user may have just allowed access in the prompt.
            Task { await prime() }
        } else if state == .restricted, lastProbeFailed {
            isRestricted = true
        }
    }
}

extension View {
    /// Points to Settings when iOS blocks network access for the app.
    func networkAccessAlert(_ access: NetworkAccess) -> some View {
        modifier(NetworkAccessAlert(access: access))
    }
}

private struct NetworkAccessAlert: ViewModifier {
    @ObservedObject var access: NetworkAccess

    func body(content: Content) -> some View {
        content.alert("Network access is turned off", isPresented: $access.isRestricted) {
            Button("Settings") {
                if let url = URL(string: UIApplication.openSettingsURLString) {
                    UIApplication.shared.open(url)
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Supa Proto cannot reach the network. In Settings, allow it to use WLAN and cellular data.")
        }
    }
}
