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
            
            // Incoming Notification Banner Overlay
            if let notif = syncService.activeBanner {
                VStack {
                    NotificationBannerView(notification: notif) {
                        withAnimation(.easeOut(duration: 0.2)) {
                            syncService.activeBanner = nil
                        }
                    }
                    .padding(.horizontal, 4)
                    .padding(.top, 2)
                    
                    Spacer()
                }
                .transition(.move(edge: .top).combined(with: .opacity))
                .zIndex(90)
            }
        }
        .onAppear {
            syncService.connectDirectToPhone()
        }
    }
}

// MARK: - Notification Banner Card View
struct NotificationBannerView: View {
    let notification: NotificationItem
    let onDismiss: () -> Void
    
    var appIcon: String {
        let pkg = notification.packageName.lowercased()
        let app = notification.appName.lowercased()
        if pkg.contains("whatsapp") || app.contains("whatsapp") { return "message.badge.filled.fill" }
        if pkg.contains("telegram") || app.contains("telegram") { return "paperplane.fill" }
        if pkg.contains("sms") || pkg.contains("mms") || pkg.contains("messaging") { return "bubble.left.and.bubble.right.fill" }
        if pkg.contains("mail") || app.contains("mail") { return "envelope.fill" }
        if pkg.contains("instagram") || app.contains("instagram") { return "camera.fill" }
        if app.contains("mac") || app.contains("bilgisayar") { return "laptopcomputer" }
        return "bell.badge.fill"
    }
    
    var bannerColor: Color {
        let pkg = notification.packageName.lowercased()
        let app = notification.appName.lowercased()
        if pkg.contains("whatsapp") { return .green }
        if pkg.contains("telegram") { return .cyan }
        if app.contains("mac") || app.contains("bilgisayar") { return .cyan }
        return .orange
    }
    
    var body: some View {
        Button(action: onDismiss) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 5) {
                    Image(systemName: appIcon)
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(bannerColor)
                    
                    Text(notification.appName.isEmpty ? "Bildirim" : notification.appName)
                        .font(.system(size: 10, weight: .bold))
                        .foregroundColor(bannerColor)
                        .lineLimit(1)
                    
                    Spacer()
                    
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 11))
                        .foregroundColor(.white.opacity(0.4))
                }
                
                if !notification.title.isEmpty {
                    Text(notification.title)
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(.primary)
                        .lineLimit(1)
                }
                
                if !notification.text.isEmpty {
                    Text(notification.text)
                        .font(.system(size: 10, weight: .medium))
                        .foregroundColor(.white.opacity(0.85))
                        .lineLimit(2)
                }
            }
            .padding(.vertical, 7)
            .padding(.horizontal, 9)
            .background(
                RoundedRectangle(cornerRadius: 14)
                    .fill(Color(white: 0.15).opacity(0.95))
                    .shadow(color: bannerColor.opacity(0.4), radius: 8, x: 0, y: 3)
                    .overlay(
                        RoundedRectangle(cornerRadius: 14)
                            .stroke(bannerColor.opacity(0.4), lineWidth: 1)
                    )
            )
        }
        .buttonStyle(.plain)
    }
}
