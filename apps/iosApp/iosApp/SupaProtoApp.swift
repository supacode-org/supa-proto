import SwiftUI
import TerminalUI

@main
struct SupaProtoApp: App {
    @StateObject private var model = AppModel()
    @Environment(\.scenePhase) private var scenePhase

    init() {
        TerminalAppearance.apply()
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(model)
                // Light or dark, following the system appearance.
                // Toolbar and link tints are text and icons, so they use the text green.
                // Set inside terminalTheme(), whose own accent tint would otherwise win.
                .tint(TerminalColors.accentText)
                .terminalTheme()
                .task { await model.observe() }
                // Ask for network access right after installation (see NetworkAccess).
                .task { await model.networkAccess.prime() }
                .onChange(of: scenePhase) { phase in
                    if phase == .active { Task { await model.networkAccess.prime() } }
                }
                .networkAccessAlert(model.networkAccess)
                .onOpenURL { model.open($0) }
                .sheet(item: $model.pendingImport) { imported in
                    NavigationStack {
                        ServerEditorView(profile: nil, imported: imported.link)
                    }
                    .environmentObject(model)
                }
                .toastWindow()
        }
    }
}
