import SwiftUI
import WatchKit

struct ContentView: View {
    @EnvironmentObject var syncService: WatchSyncService
    @State private var selectedTab: Int = 0
    
    var body: some View {
        let _ = print("📺 [ContentView] Rendered. selectedTab: \(selectedTab)")
        ZStack {
            TabView(selection: $selectedTab) {
                MediaView()
                    .tag(0)
                
                DevicesView()
                    .tag(1)
                
                RemoteActionsView()
                    .tag(2)
                
                SettingsView()
                    .tag(3)
            }
            .tabViewStyle(.page)
            
            // Incoming Call Fullscreen Overlay
            if syncService.callState.isRinging {
                IncomingCallSheet()
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                    .zIndex(100)
            }
        }
        .onAppear {
            syncService.connectDirectToPhone()
        }
    }
}
