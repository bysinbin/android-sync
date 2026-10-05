import Foundation
import SwiftUI
import WatchKit
import UserNotifications

class WatchSyncService: NSObject, ObservableObject, WKExtendedRuntimeSessionDelegate, UNUserNotificationCenterDelegate {
    static let shared = WatchSyncService()
    
    // MARK: - Published Properties
    @Published var isConnected: Bool = true
    @Published var isConnecting: Bool = false
    @Published var connectionStatusText: String = "Telefona Bağlı 🟢"
    
    @Published var serverHost: String = "127.0.0.1"
    @Published var serverPort: Int = 42426
    
    @Published var selectedSource: MediaSource = .phone
    @Published var isDirectPhoneMode: Bool = true
    @Published var macMedia: MediaInfo = MediaInfo(source: "mac")
    @Published var phoneMedia: MediaInfo = MediaInfo(source: "phone")
    @Published var deviceInfo: DeviceInfo = DeviceInfo(deviceName: "2409BRN2CA", model: "Android", batteryLevel: 52, isCharging: false)
    @Published var callState: CallState = CallState()
    @Published var isPhoneConnected: Bool = true
    @Published var isPaired: Bool = true
    @Published var pairingPin: String = "170260"
    @Published var pairedDeviceName: String = "Apple Watch"
    @Published var volumeLevel: Double = 50.0
    @Published var currentTab: Int = 0
    @Published var notifications: [NotificationItem] = []
    @Published var activeBanner: NotificationItem? = nil
    
    // MARK: - Private
    private var webSocketTask: URLSessionWebSocketTask?
    private var urlSession: URLSession
    private var reconnectTimer: Timer?
    private var statusPollTimer: Timer?
    private var isIntentionalDisconnect: Bool = false
    private var consecutiveFailures: Int = 0
    private var seenNotificationIds: Set<String> = []
    private var bannerDismissTimer: Timer?
    private var isInitialPollDone: Bool = false
    private var extendedSession: WKExtendedRuntimeSession?
    
    var activeMedia: MediaInfo {
        get {
            selectedSource == .mac ? macMedia : phoneMedia
        }
        set {
            if selectedSource == .mac {
                macMedia = newValue
            } else {
                phoneMedia = newValue
            }
        }
    }
    
    override private init() {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 4
        config.timeoutIntervalForResource = 8
        self.urlSession = URLSession(configuration: config)
        super.init()
        
        UNUserNotificationCenter.current().delegate = self
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, error in
            print("🔔 [WatchSync] Bildirim izni verildi: \(granted)")
        }
        
        connectDirectToPhone()
        startExtendedSession()
    }
    
    // MARK: - Direct Phone Connection (Ana Cihaz)
    func connectDirectToPhone() {
        self.serverHost = "127.0.0.1"
        self.serverPort = 42424
        self.selectedSource = .phone
        self.isDirectPhoneMode = true
        self.isConnected = true
        self.isPhoneConnected = true
        self.connectionStatusText = "Telefona Bağlı 🟢"
        
        // WebSocket varsa kapat (çünkü telefon HTTP REST kullanıyor)
        webSocketTask?.cancel(with: .goingAway, reason: nil)
        webSocketTask = nil
        
        startStatusPolling()
        fetchStatus()
    }
    
    // MARK: - Status Polling Timer (Tüm sekmelerde canlı senkronizasyon)
    func startStatusPolling() {
        statusPollTimer?.invalidate()
        statusPollTimer = Timer.scheduledTimer(withTimeInterval: 1.5, repeats: true) { [weak self] _ in
            self?.fetchStatus()
        }
    }
    
    // MARK: - Connection Management
    func connect() {
        if isDirectPhoneMode {
            connectDirectToPhone()
            return
        }
        
        guard !isConnecting else { return }
        disconnect(intentional: false)
        
        isConnecting = true
        connectionStatusText = "Bağlanıyor (\(serverHost):\(serverPort))..."
        
        fetchStatus { [weak self] success in
            guard let self = self else { return }
            if success {
                self.setupWebSocket()
            } else {
                self.isConnecting = false
                self.isConnected = false
                self.connectionStatusText = "Sunucuya Erişilemedi"
                self.scheduleReconnect()
            }
        }
    }
    
    func disconnect(intentional: Bool = true) {
        isIntentionalDisconnect = intentional
        reconnectTimer?.invalidate()
        reconnectTimer = nil
        
        webSocketTask?.cancel(with: .goingAway, reason: nil)
        webSocketTask = nil
        
        if !isDirectPhoneMode {
            isConnected = false
        }
        isConnecting = false
        if intentional {
            connectionStatusText = "Bağlantı Kesildi"
        }
    }
    
    private func setupWebSocket() {
        guard let url = URL(string: "ws://\(serverHost):\(serverPort)/ws") else {
            connectionStatusText = "Geçersiz Adres"
            isConnecting = false
            return
        }
        
        var request = URLRequest(url: url)
        request.timeoutInterval = 6
        
        webSocketTask = urlSession.webSocketTask(with: request)
        webSocketTask?.resume()
        
        listenWebSocket()
        
        DispatchQueue.main.async {
            self.isConnected = true
            self.isConnecting = false
            self.connectionStatusText = "Bağlandı (Mac & Saat)"
        }
    }
    
    private func listenWebSocket() {
        webSocketTask?.receive { [weak self] result in
            guard let self = self else { return }
            
            switch result {
            case .success(let message):
                switch message {
                case .string(let text):
                    self.handleIncomingWebSocketText(text)
                case .data(let data):
                    if let text = String(data: data, encoding: .utf8) {
                        self.handleIncomingWebSocketText(text)
                    }
                @unknown default:
                    break
                }
                self.listenWebSocket()
                
            case .failure(let error):
                print("WebSocket receive error: \(error)")
                if !self.isDirectPhoneMode {
                    DispatchQueue.main.async {
                        self.isConnected = false
                        self.isConnecting = false
                        self.connectionStatusText = "Bağlantı Koptu"
                        if !self.isIntentionalDisconnect {
                            self.scheduleReconnect()
                        }
                    }
                }
            }
        }
    }
    
    private func scheduleReconnect() {
        reconnectTimer?.invalidate()
        reconnectTimer = Timer.scheduledTimer(withTimeInterval: 5.0, repeats: false) { [weak self] _ in
            self?.connect()
        }
    }
    
    private func handleIncomingWebSocketText(_ text: String) {
        guard let data = text.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let event = json["event"] as? String else {
            return
        }
        
        DispatchQueue.main.async {
            switch event {
            case "media_info":
                if let payloadData = try? JSONSerialization.data(withJSONObject: json["payload"] ?? [:]),
                   let info = try? JSONDecoder().decode(MediaInfo.self, from: payloadData) {
                    if info.source == "mac" {
                        self.macMedia = info
                    } else {
                        self.phoneMedia = info
                    }
                }
            case "device_info":
                if let payloadData = try? JSONSerialization.data(withJSONObject: json["payload"] ?? [:]),
                   let dev = try? JSONDecoder().decode(DeviceInfo.self, from: payloadData) {
                    self.deviceInfo = dev
                }
            case "call_state":
                if let payloadData = try? JSONSerialization.data(withJSONObject: json["payload"] ?? [:]),
                   let call = try? JSONDecoder().decode(CallState.self, from: payloadData) {
                    self.callState = call
                }
            default:
                break
            }
        }
    }
    
    // MARK: - Fetch Status (REST)
    func fetchStatus(completion: ((Bool) -> Void)? = nil) {
        var components = URLComponents()
        components.scheme = "http"
        components.host = serverHost
        components.port = serverPort
        components.path = "/status"
        
        guard let url = components.url else {
            completion?(false)
            return
        }
        
        var request = URLRequest(url: url)
        request.timeoutInterval = 3.0
        print("🌐 [WatchSyncService] fetchStatus URL: \(url)")
        
        urlSession.dataTask(with: request) { [weak self] data, response, error in
            guard let self = self else { return }
            
            if let httpRes = response as? HTTPURLResponse {
                print("🌐 [WatchSyncService] Status HTTP \(httpRes.statusCode), bytes: \(data?.count ?? 0)")
            } else if let err = error {
                print("❌ [WatchSyncService] Status error: \(err)")
            }
            
            if let httpRes = response as? HTTPURLResponse, httpRes.statusCode == 200, let data = data {
                self.consecutiveFailures = 0
                self.parseStatusData(data, completion: completion)
            } else {
                self.consecutiveFailures += 1
                if self.consecutiveFailures >= 2 {
                    let fallbackPort = (self.serverPort == 42426) ? 42424 : 42426
                    self.serverPort = fallbackPort
                    print("🔄 [WatchSyncService] Port fallback -> \(fallbackPort)")
                }
                if self.consecutiveFailures >= 6 {
                    DispatchQueue.main.async {
                        self.isConnected = false
                        self.isPhoneConnected = false
                        self.connectionStatusText = "Bağlantı Bekleniyor"
                    }
                }
                DispatchQueue.main.async {
                    completion?(false)
                }
            }
        }.resume()
    }
    
    private func parseStatusData(_ data: Data, completion: ((Bool) -> Void)? = nil) {
        do {
            let statusObj = try JSONDecoder().decode(StatusResponse.self, from: data)
            DispatchQueue.main.async {
                if let macM = statusObj.macMedia { self.macMedia = macM }
                if let phM = statusObj.phoneMedia { 
                    self.phoneMedia = phM 
                    print("🎵 [WatchSyncService] phoneMedia güncellendi: '\(phM.title)' - '\(phM.artist)' (playing: \(phM.isPlaying))")
                }
                if let dev = statusObj.deviceInfo { self.deviceInfo = dev }
                if let call = statusObj.callState { self.callState = call }
                if let paired = statusObj.isPaired { self.isPaired = paired }
                if let pin = statusObj.pairingPin, !pin.isEmpty { self.pairingPin = pin }
                if let pName = statusObj.pairedDeviceName, !pName.isEmpty { self.pairedDeviceName = pName }
                if let notifs = statusObj.notifications {
                    self.notifications = notifs
                    if !self.isInitialPollDone {
                        for n in notifs {
                            self.seenNotificationIds.insert(n.id)
                        }
                        self.isInitialPollDone = true
                    } else {
                        let unseen = notifs.filter { !self.seenNotificationIds.contains($0.id) }
                        for item in unseen.reversed() {
                            self.seenNotificationIds.insert(item.id)
                            self.triggerNotificationBanner(item)
                        }
                    }
                }
                
                self.isConnected = true
                self.isPhoneConnected = true
                self.isConnecting = false
                self.connectionStatusText = self.isDirectPhoneMode ? "Telefona Bağlı 🟢" : "Mac'e Bağlı 🟢"
                completion?(true)
            }
        } catch {
            print("❌ [WatchSyncService] DECODE FAILED: \(error)")
            DispatchQueue.main.async { completion?(false) }
        }
    }
    
    // MARK: - Notification Handling
    func triggerNotificationBanner(_ item: NotificationItem) {
        print("🔔 [WatchSyncService] Yeni bildirim geldi: [\(item.appName)] \(item.title) - \(item.text)")
        WKInterfaceDevice.current().play(.notification)
        postNativeWatchNotification(item)
        
        withAnimation(.spring(response: 0.35, dampingFraction: 0.75)) {
            self.activeBanner = item
        }
        bannerDismissTimer?.invalidate()
        bannerDismissTimer = Timer.scheduledTimer(withTimeInterval: 5.5, repeats: false) { [weak self] _ in
            withAnimation(.easeOut(duration: 0.25)) {
                self?.activeBanner = nil
            }
        }
    }
    
    func postNativeWatchNotification(_ item: NotificationItem) {
        let content = UNMutableNotificationContent()
        let app = item.appName.isEmpty ? "Bildirim" : item.appName
        content.title = app
        if !item.title.isEmpty && item.title != app {
            content.subtitle = item.title
        }
        content.body = item.text
        content.sound = .default
        
        let trigger = UNTimeIntervalNotificationTrigger(timeInterval: 0.1, repeats: false)
        let request = UNNotificationRequest(identifier: item.id, content: content, trigger: trigger)
        UNUserNotificationCenter.current().add(request) { error in
            if let err = error {
                print("❌ [WatchSync] Native bildirim ekleme hatası: \(err)")
            } else {
                print("✅ [WatchSync] Native sistem bildirimi yayınlandı!")
            }
        }
    }
    
    func clearAllNotifications() {
        WKInterfaceDevice.current().play(.click)
        withAnimation {
            self.notifications.removeAll()
            self.activeBanner = nil
        }
        sendRestCommand(path: "/notification/clear", params: [:])
    }
    
    func dismissNotification(id: String) {
        withAnimation {
            self.notifications.removeAll { $0.id == id }
            if self.activeBanner?.id == id {
                self.activeBanner = nil
            }
        }
        sendRestCommand(path: "/notification/dismiss", params: ["id": id])
    }
    
    // MARK: - UNUserNotificationCenterDelegate
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .sound, .badge])
    }
    
    // MARK: - Extended Runtime Session (Arka Plan Canlı Tutma)
    func startExtendedSession() {
        if extendedSession == nil || extendedSession?.state == .invalid {
            extendedSession = WKExtendedRuntimeSession()
            extendedSession?.delegate = self
            extendedSession?.start()
            print("⌚ [WatchSync] WKExtendedRuntimeSession başlatıldı.")
        }
    }
    
    func extendedRuntimeSessionDidStart(_ extendedRuntimeSession: WKExtendedRuntimeSession) {
        print("⌚ [WatchSync] Arka plan sürekli çalışma oturumu devrede.")
    }
    
    func extendedRuntimeSessionWillExpire(_ extendedRuntimeSession: WKExtendedRuntimeSession) {
        print("⌚ [WatchSync] Arka plan oturumu yenileniyor...")
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) { [weak self] in
            self?.startExtendedSession()
        }
    }
    
    func extendedRuntimeSession(_ extendedRuntimeSession: WKExtendedRuntimeSession, didInvalidateWith reason: WKExtendedRuntimeSessionInvalidationReason, error: Error?) {
        print("⌚ [WatchSync] Arka plan oturumu sonlandı (reason: \(reason)). Yeniden başlatılıyor...")
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.0) { [weak self] in
            self?.startExtendedSession()
        }
    }

    // MARK: - Media Controls
    func playPause() {
        WKInterfaceDevice.current().play(.click)
        if selectedSource == .mac && !isDirectPhoneMode {
            sendRestCommand(path: "/media/command", params: ["action": "play_pause"])
            macMedia.isPlaying.toggle()
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "play_pause"])
            phoneMedia.isPlaying.toggle()
        }
    }
    
    func nextTrack() {
        WKInterfaceDevice.current().play(.click)
        if selectedSource == .mac && !isDirectPhoneMode {
            sendRestCommand(path: "/media/command", params: ["action": "next"])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "next"])
        }
    }
    
    func previousTrack() {
        WKInterfaceDevice.current().play(.click)
        if selectedSource == .mac && !isDirectPhoneMode {
            sendRestCommand(path: "/media/command", params: ["action": "previous"])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "previous"])
        }
    }
    
    func seek(percent: Double) {
        let clamped = max(0.0, min(1.0, percent))
        let pct100 = clamped * 100.0
        let pctStr = String(format: "%.1f", pct100)
        print("⌚ [WatchSyncService] Seek: clamped=\(clamped), pct100=\(pctStr), source=\(selectedSource)")
        if selectedSource == .mac && !isDirectPhoneMode {
            sendRestCommand(path: "/media/command", params: ["action": "SEEK_PERCENT", "percent": pctStr])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "SEEK_PERCENT:\(pctStr)", "percent": pctStr])
        }
    }
    
    func adjustVolume(by delta: Double) {
        volumeLevel = max(0, min(100, volumeLevel + delta))
        let action = delta > 0 ? "volume_up" : "volume_down"
        if selectedSource == .mac && !isDirectPhoneMode {
            sendRestCommand(path: "/media/command", params: ["action": action])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": action])
        }
    }
    
    // MARK: - Remote Actions
    func lockMac() {
        WKInterfaceDevice.current().play(.success)
        sendMacCommand(path: "/remote/action", params: ["action": "lock"])
    }
    
    func sleepMac() {
        WKInterfaceDevice.current().play(.success)
        sendMacCommand(path: "/remote/action", params: ["action": "sleep"])
    }
    
    func ringPhone() {
        WKInterfaceDevice.current().play(.notification)
        sendRestCommand(path: "/phone/command", params: ["action": "ring"])
    }
    
    func stopRingPhone() {
        WKInterfaceDevice.current().play(.click)
        sendRestCommand(path: "/phone/command", params: ["action": "stop_ring"])
    }
    
    func setRingerMode(_ mode: String) { // normal, silent, vibrate
        WKInterfaceDevice.current().play(.click)
        sendRestCommand(path: "/ringer/set", params: ["mode": mode])
    }
    
    func muteAll() {
        WKInterfaceDevice.current().play(.click)
        setRingerMode("silent")
        sendMacCommand(path: "/media/command", params: ["action": "volume_down"])
    }
    
    func answerCall() {
        WKInterfaceDevice.current().play(.success)
        sendRestCommand(path: "/call/action", params: ["action": "ANSWER"])
    }
    
    func rejectCall() {
        WKInterfaceDevice.current().play(.directionDown)
        sendRestCommand(path: "/call/action", params: ["action": "REJECT"])
    }
    
    // MARK: - Pairing Actions
    func confirmPairing(pin: String, completion: ((Bool, String) -> Void)? = nil) {
        WKInterfaceDevice.current().play(.click)
        let effectivePin = pin.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? pairingPin : pin.trimmingCharacters(in: .whitespacesAndNewlines)
        
        var components = URLComponents()
        components.scheme = "http"
        components.host = serverHost
        components.port = serverPort
        components.path = "/pair/confirm"
        components.queryItems = [
            URLQueryItem(name: "pin", value: effectivePin),
            URLQueryItem(name: "device_name", value: "Apple Watch")
        ]
        
        guard let url = components.url else {
            completion?(false, "Geçersiz URL")
            return
        }
        
        var request = URLRequest(url: url)
        request.timeoutInterval = 4
        
        urlSession.dataTask(with: request) { [weak self] data, response, error in
            guard let self = self else { return }
            if let httpRes = response as? HTTPURLResponse, httpRes.statusCode == 200 {
                DispatchQueue.main.async {
                    self.isPaired = true
                    if !effectivePin.isEmpty {
                        self.pairingPin = effectivePin
                    }
                    WKInterfaceDevice.current().play(.success)
                    self.fetchStatus()
                    completion?(true, "Eşleştirme başarıyla tamamlandı!")
                }
            } else {
                DispatchQueue.main.async {
                    WKInterfaceDevice.current().play(.failure)
                    completion?(false, "PIN doğrulanamadı!")
                }
            }
        }.resume()
    }
    
    func resetPairing(completion: ((Bool, String) -> Void)? = nil) {
        WKInterfaceDevice.current().play(.click)
        var components = URLComponents()
        components.scheme = "http"
        components.host = serverHost
        components.port = serverPort
        components.path = "/pair/reset"
        
        guard let url = components.url else { return }
        
        var req = URLRequest(url: url)
        req.timeoutInterval = 4
        urlSession.dataTask(with: req) { [weak self] data, response, error in
            guard let self = self, let data = data else { return }
            if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let newPin = json["pairing_pin"] as? String {
                DispatchQueue.main.async {
                    self.pairingPin = newPin
                    self.isPaired = false
                    WKInterfaceDevice.current().play(.notification)
                    completion?(true, newPin)
                }
            }
        }.resume()
    }

    // MARK: - Generic REST Request Helper
    private func sendRestCommand(path: String, params: [String: String]) {
        var components = URLComponents()
        components.scheme = "http"
        components.host = serverHost
        components.port = serverPort
        components.path = path
        components.queryItems = params.map { URLQueryItem(name: $0.key, value: $0.value) }
        
        guard let url = components.url else { return }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 3
        
        urlSession.dataTask(with: request) { _, _, _ in }.resume()
    }
    
    private func sendMacCommand(path: String, params: [String: String]) {
        var components = URLComponents()
        components.scheme = "http"
        components.host = "127.0.0.1"
        components.port = 42424
        components.path = path
        components.queryItems = params.map { URLQueryItem(name: $0.key, value: $0.value) }
        
        guard let url = components.url else { return }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 3
        
        urlSession.dataTask(with: request) { _, _, _ in }.resume()
    }
}
