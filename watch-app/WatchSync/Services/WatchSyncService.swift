import Foundation
import SwiftUI
import WatchKit

class WatchSyncService: ObservableObject {
    static let shared = WatchSyncService()
    
    // MARK: - Published Properties
    @Published var isConnected: Bool = false
    @Published var isConnecting: Bool = false
    @Published var connectionStatusText: String = "Bağlantı Bekleniyor"
    
    @Published var serverHost: String {
        didSet {
            UserDefaults.standard.set(serverHost, forKey: "sync_server_host")
        }
    }
    
    @Published var serverPort: Int {
        didSet {
            UserDefaults.standard.set(serverPort, forKey: "sync_server_port")
        }
    }
    
    @Published var selectedSource: MediaSource = .phone
    @Published var isDirectPhoneMode: Bool = true
    @Published var macMedia: MediaInfo = MediaInfo(source: "mac")
    @Published var phoneMedia: MediaInfo = MediaInfo(source: "phone")
    @Published var deviceInfo: DeviceInfo = DeviceInfo()
    @Published var callState: CallState = CallState()
    @Published var isPhoneConnected: Bool = false
    @Published var isPaired: Bool = false
    @Published var pairingPin: String = ""
    @Published var pairedDeviceName: String = ""
    @Published var volumeLevel: Double = 50.0
    
    // MARK: - Private
    private var webSocketTask: URLSessionWebSocketTask?
    private var urlSession: URLSession
    private var reconnectTimer: Timer?
    private var isIntentionalDisconnect: Bool = false
    
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
    
    private init() {
        let savedHost = UserDefaults.standard.string(forKey: "sync_server_host") ?? "127.0.0.1"
        let savedPort = UserDefaults.standard.integer(forKey: "sync_server_port")
        
        self.serverHost = savedHost
        self.serverPort = savedPort > 0 ? savedPort : 42424
        
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 5
        config.timeoutIntervalForResource = 10
        self.urlSession = URLSession(configuration: config)
        
        connect()
    }
    
    // MARK: - Direct Phone Connection
    func connectDirectToPhone() {
        WKInterfaceDevice.current().play(.click)
        self.serverHost = "127.0.0.1"
        self.serverPort = 42426
        self.selectedSource = .phone
        self.isDirectPhoneMode = true
        connect()
    }

    // MARK: - Connection Management
    func connect() {
        guard !isConnecting else { return }
        disconnect(intentional: false)
        
        isConnecting = true
        connectionStatusText = "Bağlanıyor (\(serverHost):\(serverPort))..."
        
        // Önce hızlı REST /status kontrolü yap
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
        
        isConnected = false
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
        request.timeoutInterval = 10
        
        webSocketTask = urlSession.webSocketTask(with: request)
        webSocketTask?.resume()
        
        sendAuthPayload()
        listenWebSocket()
        
        DispatchQueue.main.async {
            self.isConnected = true
            self.isConnecting = false
            self.connectionStatusText = "Bağlandı (Mac & Saat)"
            WKInterfaceDevice.current().play(.click)
        }
    }
    
    private func sendAuthPayload() {
        let authObj: [String: Any] = [
            "event": "auth_request",
            "payload": [
                "client_id": "apple-watch-\(WKInterfaceDevice.current().name)",
                "client_name": "Apple Watch",
                "os": "watchOS",
                "pairing_pin": ""
            ]
        ]
        
        if let data = try? JSONSerialization.data(withJSONObject: authObj),
           let str = String(data: data, encoding: .utf8) {
            webSocketTask?.send(.string(str)) { error in
                if let err = error {
                    print("Auth send error: \(err)")
                }
            }
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
    
    private func scheduleReconnect() {
        reconnectTimer?.invalidate()
        reconnectTimer = Timer.scheduledTimer(withTimeInterval: 5.0, repeats: false) { [weak self] _ in
            self?.connect()
        }
    }
    
    // MARK: - Incoming Message Handler
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
                    if info.source == "phone" {
                        self.phoneMedia = info
                    } else {
                        self.macMedia = info
                    }
                }
                
            case "device_info":
                if let payloadData = try? JSONSerialization.data(withJSONObject: json["payload"] ?? [:]),
                   let dev = try? JSONDecoder().decode(DeviceInfo.self, from: payloadData) {
                    self.deviceInfo = dev
                    self.isPhoneConnected = true
                }
                
            case "call_state":
                if let payloadData = try? JSONSerialization.data(withJSONObject: json["payload"] ?? [:]),
                   let call = try? JSONDecoder().decode(CallState.self, from: payloadData) {
                    let oldRinging = self.callState.isRinging
                    self.callState = call
                    if call.isRinging && !oldRinging {
                        WKInterfaceDevice.current().play(.notification)
                    }
                }
                
            case "auth_request":
                if let payload = json["payload"] as? [String: Any] {
                    if let pin = payload["pairing_pin"] as? String {
                        self.pairingPin = pin
                    }
                }
                
            case "pair_confirm":
                if let payload = json["payload"] as? [String: Any] {
                    let approved = payload["approved"] as? Bool ?? false
                    if approved {
                        self.isPaired = true
                        if let devName = payload["client_name"] as? String {
                            self.pairedDeviceName = devName
                        }
                        WKInterfaceDevice.current().play(.success)
                    }
                }
                
            case "ping":
                let pong = "{\"event\":\"pong\",\"payload\":{}}"
                self.webSocketTask?.send(.string(pong)) { _ in }
                
            default:
                break
            }
        }
    }
    
    // MARK: - REST API Fetch
    func fetchStatus(completion: ((Bool) -> Void)? = nil) {
        guard let url = URL(string: "http://\(serverHost):\(serverPort)/status") else {
            completion?(false)
            return
        }
        
        urlSession.dataTask(with: url) { [weak self] data, response, error in
            guard let self = self else { return }
            
            // Simulator veya LAN IP erişim engeli durumunda 127.0.0.1 ile otomatik dene
            if (error != nil || (response as? HTTPURLResponse)?.statusCode != 200) && self.serverHost != "127.0.0.1" {
                if let fallbackUrl = URL(string: "http://127.0.0.1:\(self.serverPort)/status") {
                    self.urlSession.dataTask(with: fallbackUrl) { [weak self] fbData, fbRes, fbErr in
                        guard let self = self, let fbData = fbData, fbErr == nil,
                              (fbRes as? HTTPURLResponse)?.statusCode == 200 else {
                            DispatchQueue.main.async { completion?(false) }
                            return
                        }
                        self.serverHost = "127.0.0.1"
                        self.parseStatusData(fbData, completion: completion)
                    }.resume()
                    return
                }
            }
            
            guard let data = data, error == nil else {
                DispatchQueue.main.async { completion?(false) }
                return
            }
            
            self.parseStatusData(data, completion: completion)
        }.resume()
    }
    
    private func parseStatusData(_ data: Data, completion: ((Bool) -> Void)? = nil) {
        if let statusObj = try? JSONDecoder().decode(StatusResponse.self, from: data) {
            DispatchQueue.main.async {
                if let macM = statusObj.macMedia { self.macMedia = macM }
                if let phM = statusObj.phoneMedia { self.phoneMedia = phM }
                if let dev = statusObj.deviceInfo { self.deviceInfo = dev }
                if let call = statusObj.callState { self.callState = call }
                if let paired = statusObj.isPaired { self.isPaired = paired }
                if let pin = statusObj.pairingPin, !pin.isEmpty { self.pairingPin = pin }
                if let pName = statusObj.pairedDeviceName, !pName.isEmpty { self.pairedDeviceName = pName }
                self.isPhoneConnected = (statusObj.connected == true)
                completion?(true)
            }
        } else {
            DispatchQueue.main.async { completion?(false) }
        }
    }
    
    // MARK: - Media Controls
    func playPause() {
        WKInterfaceDevice.current().play(.click)
        if selectedSource == .mac {
            sendRestCommand(path: "/media/command", params: ["action": "play_pause"])
            macMedia.isPlaying.toggle()
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "play_pause"])
            phoneMedia.isPlaying.toggle()
        }
    }
    
    func nextTrack() {
        WKInterfaceDevice.current().play(.click)
        if selectedSource == .mac {
            sendRestCommand(path: "/media/command", params: ["action": "next"])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "next"])
        }
    }
    
    func previousTrack() {
        WKInterfaceDevice.current().play(.click)
        if selectedSource == .mac {
            sendRestCommand(path: "/media/command", params: ["action": "previous"])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "previous"])
        }
    }
    
    func seek(percent: Double) {
        let clamped = max(0.0, min(1.0, percent))
        if selectedSource == .mac {
            sendRestCommand(path: "/media/command", params: ["action": "seek_percent", "percent": String(format: "%.3f", clamped)])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": "seek_percent", "percent": String(format: "%.3f", clamped)])
        }
    }
    
    func adjustVolume(by delta: Double) {
        volumeLevel = max(0, min(100, volumeLevel + delta))
        let action = delta > 0 ? "volume_up" : "volume_down"
        if selectedSource == .mac {
            sendRestCommand(path: "/media/command", params: ["action": action])
        } else {
            sendRestCommand(path: "/phone/command", params: ["action": action])
        }
    }
    
    // MARK: - Remote Actions
    func lockMac() {
        WKInterfaceDevice.current().play(.success)
        sendRestCommand(path: "/remote/action", params: ["action": "lock"])
    }
    
    func sleepMac() {
        WKInterfaceDevice.current().play(.success)
        sendRestCommand(path: "/remote/action", params: ["action": "sleep"])
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
        // Mac sesini kapat
        adjustVolume(by: -100)
        // Telefonu sessize al
        setRingerMode("silent")
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
        
        urlSession.dataTask(with: requestFromUrl(url)) { [weak self] data, response, error in
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
    
    private func requestFromUrl(_ url: URL) -> URLRequest {
        var req = URLRequest(url: url)
        req.timeoutInterval = 4
        return req
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
}
