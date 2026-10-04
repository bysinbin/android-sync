import SwiftUI
import WatchKit

struct DevicesView: View {
    @EnvironmentObject var syncService: WatchSyncService
    @State private var isRingingPhone: Bool = false
    
    var body: some View {
        ScrollView {
            VStack(spacing: 10) {
                // MARK: - Mac Status Card
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Image(systemName: "laptopcomputer")
                            .foregroundColor(.cyan)
                            .font(.system(size: 14))
                        Text("Mac Bilgisayar")
                            .font(.system(size: 13, weight: .bold))
                        Spacer()
                        Circle()
                            .fill(syncService.isConnected ? Color.green : Color.red)
                            .frame(width: 7, height: 7)
                    }
                    
                    HStack {
                        Text(syncService.serverHost)
                            .font(.system(size: 10, design: .monospaced))
                            .foregroundColor(.secondary)
                        Spacer()
                        Text(syncService.isConnected ? "Bağlı" : "Çevrimdışı")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundColor(syncService.isConnected ? .green : .secondary)
                    }
                }
                .padding(10)
                .background(Color.white.opacity(0.1))
                .cornerRadius(12)
                
                // MARK: - Android Phone Card
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Image(systemName: "iphone")
                            .foregroundColor(.green)
                            .font(.system(size: 14))
                        Text(syncService.deviceInfo.deviceName.isEmpty ? "Android Telefon" : syncService.deviceInfo.deviceName)
                            .font(.system(size: 13, weight: .bold))
                            .lineLimit(1)
                        Spacer()
                        Circle()
                            .fill(syncService.isPhoneConnected ? Color.green : Color.orange)
                            .frame(width: 7, height: 7)
                    }
                    
                    // Battery Level Row
                    HStack(spacing: 8) {
                        HStack(spacing: 4) {
                            Image(systemName: syncService.deviceInfo.isCharging ? "battery.100.bolt" : "battery.75")
                                .foregroundColor(syncService.deviceInfo.isCharging ? .yellow : .green)
                                .font(.system(size: 14))
                            Text("%\(syncService.deviceInfo.batteryLevel)")
                                .font(.system(size: 12, weight: .bold))
                        }
                        
                        if syncService.deviceInfo.isCharging {
                            Text("Şarj Oluyor")
                                .font(.system(size: 9, weight: .medium))
                                .foregroundColor(.yellow)
                        }
                        
                        Spacer()
                    }
                    
                    Divider().background(Color.white.opacity(0.15))
                    
                    // Quick Action: Ring Phone (Find My Phone)
                    Button(action: {
                        if isRingingPhone {
                            syncService.stopRingPhone()
                            isRingingPhone = false
                        } else {
                            syncService.ringPhone()
                            isRingingPhone = true
                        }
                    }) {
                        HStack {
                            Image(systemName: isRingingPhone ? "bell.slash.fill" : "bell.fill")
                                .foregroundColor(isRingingPhone ? .red : .yellow)
                            Text(isRingingPhone ? "Çaldırmayı Durdur" : "Telefonu Çaldır")
                                .font(.system(size: 11, weight: .semibold))
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                        .background(isRingingPhone ? Color.red.opacity(0.3) : Color.yellow.opacity(0.2))
                        .cornerRadius(8)
                    }
                    .buttonStyle(.plain)
                    
                    // Ringer Modes
                    HStack(spacing: 6) {
                        RingerButton(title: "Zil", icon: "speaker.wave.2.fill", mode: "normal")
                        RingerButton(title: "Titreşim", icon: "iphone.radiowaves.left.and.right", mode: "vibrate")
                        RingerButton(title: "Sessiz", icon: "speaker.slash.fill", mode: "silent")
                    }
                }
                .padding(10)
                .background(Color.white.opacity(0.1))
                .cornerRadius(12)
            }
            .padding(.horizontal, 4)
        }
    }
}

struct RingerButton: View {
    @EnvironmentObject var syncService: WatchSyncService
    let title: String
    let icon: String
    let mode: String
    
    var body: some View {
        Button(action: {
            syncService.setRingerMode(mode)
        }) {
            VStack(spacing: 3) {
                Image(systemName: icon)
                    .font(.system(size: 11))
                Text(title)
                    .font(.system(size: 9, weight: .medium))
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 4)
            .background(Color.white.opacity(0.08))
            .cornerRadius(6)
        }
        .buttonStyle(.plain)
    }
}
