import SwiftUI
import WatchKit

struct SettingsView: View {
    @EnvironmentObject var syncService: WatchSyncService
    @StateObject private var discovery = NetworkDiscovery()
    @State private var showingManualIpAlert: Bool = false
    @State private var inputHost: String = ""
    
    var body: some View {
        ScrollView {
            VStack(spacing: 8) {
                Text("Bağlantı Ayarları")
                    .font(.system(size: 13, weight: .bold))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 4)
                
                // Current Target Box
                VStack(alignment: .leading, spacing: 4) {
                    Text("Hedef Mac Sunucu")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(.secondary)
                    
                    Text("\(syncService.serverHost):\(syncService.serverPort)")
                        .font(.system(size: 12, weight: .bold, design: .monospaced))
                        .foregroundColor(.cyan)
                    
                    Text(syncService.connectionStatusText)
                        .font(.system(size: 9))
                        .foregroundColor(syncService.isConnected ? .green : .orange)
                }
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.white.opacity(0.1))
                .cornerRadius(10)
                
                // Reconnect Button
                Button(action: {
                    WKInterfaceDevice.current().play(.click)
                    syncService.connect()
                }) {
                    HStack {
                        Image(systemName: "arrow.clockwise")
                        Text("Yeniden Bağlan")
                            .font(.system(size: 11, weight: .semibold))
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
                    .background(Color.blue)
                    .cornerRadius(8)
                }
                .buttonStyle(.plain)
                
                // Auto Discovery Section
                VStack(alignment: .leading, spacing: 6) {
                    Button(action: {
                        WKInterfaceDevice.current().play(.click)
                        if discovery.isSearching {
                            discovery.stopDiscovery()
                        } else {
                            discovery.startDiscovery()
                        }
                    }) {
                        HStack {
                            Image(systemName: discovery.isSearching ? "stop.circle.fill" : "antenna.radiowaves.left.and.right")
                                .foregroundColor(discovery.isSearching ? .red : .green)
                            Text(discovery.isSearching ? "Aramayı Durdur" : "Ağda Mac Ara")
                                .font(.system(size: 11, weight: .semibold))
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                        .background(Color.white.opacity(0.12))
                        .cornerRadius(8)
                    }
                    .buttonStyle(.plain)
                    
                    // Discovered list
                    ForEach(discovery.discoveredServers) { srv in
                        Button(action: {
                            WKInterfaceDevice.current().play(.success)
                            syncService.serverHost = srv.host
                            syncService.serverPort = srv.port
                            syncService.connect()
                            discovery.stopDiscovery()
                        }) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(srv.name)
                                    .font(.system(size: 11, weight: .bold))
                                Text("\(srv.host):\(srv.port)")
                                    .font(.system(size: 9, design: .monospaced))
                                    .foregroundColor(.secondary)
                            }
                            .padding(6)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.cyan.opacity(0.2))
                            .cornerRadius(6)
                        }
                        .buttonStyle(.plain)
                    }
                }
                
                // Info text
                Text("MacSync watchOS v1.0\nMac ve Android cihazlarınızla doğrudan Wi-Fi senkronizasyonu.")
                    .font(.system(size: 8))
                    .foregroundColor(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.top, 4)
            }
            .padding(.horizontal, 4)
        }
    }
}
