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
                
                NotificationsView()
                    .tag(1)
                
                DevicesView()
                    .tag(2)
                
                RemoteActionsView()
                    .tag(3)
                
                SettingsView()
                    .tag(4)
            }
            .tabViewStyle(.page)
            .onChange(of: selectedTab) { newTab in
                syncService.currentTab = newTab
            }
            .onChange(of: syncService.currentTab) { newTab in
                selectedTab = newTab
            }
            
            // Incoming Call Fullscreen Overlay
            if syncService.callState.isRinging {
                IncomingCallSheet()
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                    .zIndex(100)
            }
            
            // Incoming Notification Banner Overlay
            if let notif = syncService.activeBanner {
                VStack {
                    NotificationBannerView(
                        notification: notif,
                        onDismiss: {
                            withAnimation(.easeOut(duration: 0.2)) {
                                syncService.activeBanner = nil
                            }
                        },
                        onOpenTab: {
                            withAnimation {
                                syncService.activeBanner = nil
                                syncService.currentTab = 1
                            }
                        }
                    )
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

// MARK: - Notification Style Helpers
extension NotificationItem {
    var appIcon: String {
        let pkg = packageName.lowercased()
        let app = appName.lowercased()
        if pkg.contains("whatsapp") || app.contains("whatsapp") { return "message.badge.filled.fill" }
        if pkg.contains("telegram") || app.contains("telegram") { return "paperplane.fill" }
        if pkg.contains("dialer") || pkg.contains("phone") || app.contains("arama") { return "phone.fill" }
        if pkg.contains("sms") || pkg.contains("mms") || pkg.contains("messaging") { return "bubble.left.and.bubble.right.fill" }
        if pkg.contains("mail") || app.contains("mail") { return "envelope.fill" }
        if pkg.contains("instagram") || app.contains("instagram") { return "camera.fill" }
        if app.contains("mac") || app.contains("bilgisayar") { return "laptopcomputer" }
        return "bell.badge.fill"
    }
    
    var appColor: Color {
        let pkg = packageName.lowercased()
        let app = appName.lowercased()
        if pkg.contains("whatsapp") { return .green }
        if pkg.contains("telegram") { return .cyan }
        if pkg.contains("dialer") || pkg.contains("phone") || app.contains("arama") { return .green }
        if pkg.contains("sms") || pkg.contains("mms") || pkg.contains("messaging") { return .blue }
        if pkg.contains("mail") || app.contains("mail") { return .orange }
        if pkg.contains("instagram") || app.contains("instagram") { return .pink }
        if app.contains("mac") || app.contains("bilgisayar") { return .purple }
        return .orange
    }
    
    var formattedTime: String {
        guard timestamp > 0 else { return "Şimdi" }
        let date = Date(timeIntervalSince1970: Double(timestamp) / 1000.0)
        let elapsed = Int(Date().timeIntervalSince(date))
        if elapsed < 60 {
            return "Şimdi"
        } else if elapsed < 3600 {
            return "\(max(1, elapsed / 60)) dk"
        } else {
            let formatter = DateFormatter()
            formatter.dateFormat = "HH:mm"
            return formatter.string(from: date)
        }
    }
}

// MARK: - Notifications Tab View
struct NotificationsView: View {
    @EnvironmentObject var syncService: WatchSyncService
    
    var body: some View {
        ScrollView {
            LazyVStack(spacing: 8) {
                // Header Bar
                HStack(spacing: 4) {
                    Image(systemName: "bell.badge.fill")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.orange)
                    
                    Text("Bildirimler")
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(.white)
                        .lineLimit(1)
                    
                    if !syncService.notifications.isEmpty {
                        Text("\(syncService.notifications.count)")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundColor(.black)
                            .padding(.horizontal, 4)
                            .padding(.vertical, 1)
                            .background(Capsule().fill(Color.orange))
                    }
                    
                    Spacer()
                    
                    if !syncService.notifications.isEmpty {
                        Button(action: {
                            syncService.clearAllNotifications()
                        }) {
                            HStack(spacing: 3) {
                                Image(systemName: "trash")
                                    .font(.system(size: 8))
                                Text("Temizle")
                                    .font(.system(size: 8, weight: .semibold))
                            }
                            .foregroundColor(.red.opacity(0.95))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 3)
                            .background(Color.red.opacity(0.18))
                            .cornerRadius(6)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.horizontal, 4)
                .padding(.top, 2)
                
                // Content
                if syncService.notifications.isEmpty {
                    VStack(spacing: 6) {
                        Spacer(minLength: 14)
                        
                        ZStack {
                            Circle()
                                .fill(Color.orange.opacity(0.15))
                                .frame(width: 44, height: 44)
                            Image(systemName: "bell.slash.fill")
                                .font(.system(size: 20))
                                .foregroundColor(.orange)
                        }
                        .padding(.bottom, 2)
                        
                        Text("Yeni Bildirim Yok")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundColor(.white)
                        
                        Text("Telefona veya Mac'e gelen bildirimler burada listelenir.")
                            .font(.system(size: 10))
                            .foregroundColor(.gray)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 8)
                        
                        // Background status pill
                        HStack(spacing: 4) {
                            Image(systemName: "bolt.shield.fill")
                                .font(.system(size: 8))
                                .foregroundColor(.green)
                            Text("Uygulama kapalıyken de bildirim gelir")
                                .font(.system(size: 8, weight: .medium))
                                .foregroundColor(.green.opacity(0.9))
                        }
                        .padding(.horizontal, 7)
                        .padding(.vertical, 4)
                        .background(Color.green.opacity(0.12))
                        .cornerRadius(6)
                        .padding(.top, 4)
                        
                        Spacer(minLength: 14)
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 10)
                } else {
                    // List of Notification Cards
                    ForEach(Array(syncService.notifications.prefix(20))) { item in
                        NotificationRowView(notification: item) {
                            syncService.dismissNotification(id: item.id)
                        }
                    }
                    
                    // Background active footer
                    HStack(spacing: 4) {
                        Circle()
                            .fill(Color.green)
                            .frame(width: 5, height: 5)
                        Text("Arka plan bildirim dinleyicisi devrede")
                            .font(.system(size: 8))
                            .foregroundColor(.gray)
                    }
                    .padding(.top, 2)
                    .padding(.bottom, 8)
                }
            }
            .padding(.horizontal, 2)
        }
    }
}

// MARK: - Notification Row Card View
struct NotificationRowView: View {
    let notification: NotificationItem
    let onDismiss: () -> Void
    
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            // Header Row: App icon, App name, Time, Dismiss button
            HStack(spacing: 4) {
                Image(systemName: notification.appIcon)
                    .font(.system(size: 10, weight: .bold))
                    .foregroundColor(notification.appColor)
                
                Text(notification.appName.isEmpty ? "Bildirim" : notification.appName)
                    .font(.system(size: 10, weight: .bold))
                    .foregroundColor(notification.appColor)
                    .lineLimit(1)
                
                Spacer()
                
                Text(notification.formattedTime)
                    .font(.system(size: 9))
                    .foregroundColor(.gray)
                
                Button(action: onDismiss) {
                    Image(systemName: "xmark")
                        .font(.system(size: 8, weight: .bold))
                        .foregroundColor(.white.opacity(0.5))
                        .padding(4)
                        .background(Color.white.opacity(0.1))
                        .clipShape(Circle())
                }
                .buttonStyle(.plain)
            }
            
            // Notification Title
            if !notification.title.isEmpty {
                Text(notification.title)
                    .font(.system(size: 11, weight: .bold))
                    .foregroundColor(.white)
                    .lineLimit(1)
            }
            
            // Notification Body Text
            if !notification.text.isEmpty {
                Text(notification.text)
                    .font(.system(size: 10, weight: .regular))
                    .foregroundColor(.white.opacity(0.85))
                    .lineLimit(3)
            }
        }
        .padding(8)
        .background(
            RoundedRectangle(cornerRadius: 12)
                .fill(Color(white: 0.12))
                .overlay(
                    RoundedRectangle(cornerRadius: 12)
                        .stroke(notification.appColor.opacity(0.35), lineWidth: 1)
                )
        )
    }
}

// MARK: - Notification Banner Card View (Ekran içi anlık bildirim)
struct NotificationBannerView: View {
    let notification: NotificationItem
    let onDismiss: () -> Void
    let onOpenTab: () -> Void
    
    var body: some View {
        Button(action: onOpenTab) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 5) {
                    Image(systemName: notification.appIcon)
                        .font(.system(size: 11, weight: .bold))
                        .foregroundColor(notification.appColor)
                    
                    Text(notification.appName.isEmpty ? "Bildirim" : notification.appName)
                        .font(.system(size: 10, weight: .bold))
                        .foregroundColor(notification.appColor)
                        .lineLimit(1)
                    
                    Spacer()
                    
                    Button(action: onDismiss) {
                        Image(systemName: "xmark.circle.fill")
                            .font(.system(size: 11))
                            .foregroundColor(.white.opacity(0.5))
                    }
                    .buttonStyle(.plain)
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
                    .shadow(color: notification.appColor.opacity(0.4), radius: 8, x: 0, y: 3)
                    .overlay(
                        RoundedRectangle(cornerRadius: 14)
                            .stroke(notification.appColor.opacity(0.4), lineWidth: 1)
                    )
            )
        }
        .buttonStyle(.plain)
    }
}
