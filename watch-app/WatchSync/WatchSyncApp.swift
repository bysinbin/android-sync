import SwiftUI

@main
struct WatchSyncApp: App {
    @StateObject private var syncService = WatchSyncService.shared
    @Environment(\.scenePhase) private var scenePhase
    
    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(syncService)
        }
        .onChange(of: scenePhase) { newPhase in
            if newPhase == .active {
                syncService.fetchStatus()
                if !syncService.isConnected {
                    syncService.connect()
                }
            }
        }
    }
}
