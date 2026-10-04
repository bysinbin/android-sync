import SwiftUI
import WatchKit

struct RemoteActionsView: View {
    @EnvironmentObject var syncService: WatchSyncService
    
    var body: some View {
        ScrollView {
            VStack(spacing: 8) {
                Text("Hızlı Eylemler")
                    .font(.system(size: 13, weight: .bold))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 4)
                
                // Mac Lock Button
                ActionButton(
                    title: "Mac'i Kilitle",
                    subtitle: "Ekranı anında kilitler",
                    icon: "lock.fill",
                    color: .orange
                ) {
                    syncService.lockMac()
                }
                
                // Mac Sleep Button
                ActionButton(
                    title: "Mac'i Uyut",
                    subtitle: "Uyku moduna alır",
                    icon: "moon.stars.fill",
                    color: .indigo
                ) {
                    syncService.sleepMac()
                }
                
                // Dual Device Mute All
                ActionButton(
                    title: "Tümünü Sessize Al",
                    subtitle: "Mac & Telefon aynı anda",
                    icon: "speaker.slash.fill",
                    color: .red
                ) {
                    syncService.muteAll()
                }
                
                // Refresh Status
                ActionButton(
                    title: "Ekosistemi Yenile",
                    subtitle: "Mac + Telefon anlık senkron",
                    icon: "arrow.triangle.2.circlepath",
                    color: .blue
                ) {
                    WKInterfaceDevice.current().play(.click)
                    syncService.fetchStatus()
                }
            }
            .padding(.horizontal, 4)
        }
    }
}

struct ActionButton: View {
    let title: String
    let subtitle: String
    let icon: String
    let color: Color
    let action: () -> Void
    
    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: icon)
                    .font(.system(size: 16))
                    .foregroundColor(color)
                    .frame(width: 32, height: 32)
                    .background(color.opacity(0.2))
                    .clipShape(Circle())
                
                VStack(alignment: .leading, spacing: 1) {
                    Text(title)
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(.primary)
                    Text(subtitle)
                        .font(.system(size: 9))
                        .foregroundColor(.secondary)
                }
                Spacer()
            }
            .padding(8)
            .background(Color.white.opacity(0.1))
            .cornerRadius(10)
        }
        .buttonStyle(.plain)
    }
}
