import SwiftUI
import WatchKit

struct DevicesView: View {
    @EnvironmentObject var syncService: WatchSyncService
    @State private var isRingingPhone: Bool = false
    
    var isPhoneActive: Bool {
        syncService.isPhoneConnected || syncService.isConnected
    }
    
    var body: some View {
        ScrollView {
            VStack(spacing: 8) {
                // MARK: - Android Phone Card (Ana Cihaz)
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Image(systemName: "iphone")
                            .foregroundColor(.green)
                            .font(.system(size: 13))
                        Text(syncService.deviceInfo.deviceName.isEmpty ? "Android Telefon" : syncService.deviceInfo.deviceName)
                            .font(.system(size: 12, weight: .bold))
                            .lineLimit(1)
                        Spacer()
                        HStack(spacing: 3) {
                            Circle()
                                .fill(isPhoneActive ? Color.green : Color.orange)
                                .frame(width: 6, height: 6)
                            Text(isPhoneActive ? "Bağlı" : "Aranıyor")
                                .font(.system(size: 8, weight: .bold))
                                .foregroundColor(isPhoneActive ? .green : .orange)
                        }
                    }
                    
                    // Battery Level Row & Ana Cihaz Badge
                    HStack(spacing: 6) {
                        HStack(spacing: 3) {
                            Image(systemName: syncService.deviceInfo.isCharging ? "battery.100.bolt" : "battery.75")
                                .foregroundColor(syncService.deviceInfo.isCharging ? .yellow : .green)
                                .font(.system(size: 13))
                            Text("%\(syncService.deviceInfo.batteryLevel)")
                                .font(.system(size: 11, weight: .bold))
                        }
                        
                        if syncService.deviceInfo.isCharging {
                            Text("Şarj Oluyor")
                                .font(.system(size: 8, weight: .medium))
                                .foregroundColor(.yellow)
                        }
                        
                        Spacer()
                        
                        Text("ANA CİHAZ ⚡")
                            .font(.system(size: 7, weight: .black))
                            .padding(.horizontal, 5)
                            .padding(.vertical, 2)
                            .background(Color.green.opacity(0.25))
                            .cornerRadius(4)
                            .foregroundColor(.green)
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
                                .font(.system(size: 11))
                            Text(isRingingPhone ? "Çaldırmayı Durdur" : "Telefonu Çaldır")
                                .font(.system(size: 10, weight: .semibold))
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 5)
                        .background(isRingingPhone ? Color.red.opacity(0.3) : Color.yellow.opacity(0.2))
                        .cornerRadius(8)
                    }
                    .buttonStyle(.plain)
                    
                    // Ringer Modes
                    HStack(spacing: 4) {
                        RingerButton(title: "Zil", icon: "speaker.wave.2.fill", mode: "normal")
                        RingerButton(title: "Titreşim", icon: "iphone.radiowaves.left.and.right", mode: "vibrate")
                        RingerButton(title: "Sessiz", icon: "speaker.slash.fill", mode: "silent")
                    }
                }
                .padding(8)
                .background(Color.white.opacity(0.1))
                .cornerRadius(12)
                
                // MARK: - Mac Status Card (İkincil)
                VStack(alignment: .leading, spacing: 5) {
                    HStack {
                        Image(systemName: "laptopcomputer")
                            .foregroundColor(.cyan)
                            .font(.system(size: 12))
                        Text("Mac Bilgisayar")
                            .font(.system(size: 11, weight: .bold))
                        Spacer()
                        Circle()
                            .fill(Color.green)
                            .frame(width: 6, height: 6)
                        Text("Bağlı")
                            .font(.system(size: 8, weight: .semibold))
                            .foregroundColor(.green)
                    }
                    
                    HStack {
                        Text("192.168.50.96:42424")
                            .font(.system(size: 8, design: .monospaced))
                            .foregroundColor(.secondary)
                        Spacer()
                        Text("İkincil Cihaz")
                            .font(.system(size: 8, weight: .medium))
                            .foregroundColor(.cyan)
                    }
                }
                .padding(8)
                .background(Color.white.opacity(0.08))
                .cornerRadius(10)
            }
            .padding(.horizontal, 4)
        }
        .onAppear {
            syncService.fetchStatus()
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
            VStack(spacing: 2) {
                Image(systemName: icon)
                    .font(.system(size: 10))
                Text(title)
                    .font(.system(size: 8, weight: .medium))
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 4)
            .background(Color.white.opacity(0.08))
            .cornerRadius(6)
        }
        .buttonStyle(.plain)
    }
}
