import Foundation
import Network

enum TargetDeviceType: String, Codable {
    case mac = "mac"
    case pc = "pc"
    case phone = "phone"
    
    var title: String {
        switch self {
        case .mac: return "Mac Bilgisayar"
        case .pc: return "Windows PC"
        case .phone: return "Android Telefon"
        }
    }
    
    var iconName: String {
        switch self {
        case .mac: return "laptopcomputer"
        case .pc: return "desktopcomputer"
        case .phone: return "iphone"
        }
    }
}

struct DiscoveredTarget: Identifiable, Hashable {
    var id: String
    var name: String
    var model: String
    var host: String
    var port: Int
    var type: TargetDeviceType
    var batteryLevel: Int?
    var isCharging: Bool?
    var isPaired: Bool = false
    var pairingPin: String?
}

struct DiscoveredServer: Identifiable, Hashable {
    let id = UUID()
    let name: String
    let host: String
    let port: Int
}

class NetworkDiscovery: ObservableObject {
    @Published var discoveredTargets: [DiscoveredTarget] = []
    @Published var discoveredServers: [DiscoveredServer] = []
    @Published var activePairingPin: String = ""
    @Published var isPaired: Bool = false
    @Published var isSearching: Bool = false
    @Published var searchStatus: String = "Hazır"
    
    private var broadcastConnection: NWConnection?
    private let discoveryPort: NWEndpoint.Port = 42425
    private var probeTimer: Timer?
    
    var discoveredPhones: [DiscoveredTarget] {
        discoveredTargets.filter { $0.type == .phone }
    }
    
    var discoveredComputers: [DiscoveredTarget] {
        discoveredTargets.filter { $0.type != .phone }
    }
    
    func startDiscovery() {
        guard !isSearching else { return }
        isSearching = true
        searchStatus = "Cihazlar taranıyor..."
        
        probeKnownEndpoints()
        sendBroadcastRequest()
        
        probeTimer?.invalidate()
        probeTimer = Timer.scheduledTimer(withTimeInterval: 4.0, repeats: true) { [weak self] _ in
            self?.probeKnownEndpoints()
            self?.sendBroadcastRequest()
        }
    }
    
    func stopDiscovery() {
        isSearching = false
        searchStatus = "Arama durduruldu"
        probeTimer?.invalidate()
        probeTimer = nil
        broadcastConnection?.cancel()
        broadcastConnection = nil
    }
    
    private func sendBroadcastRequest() {
        broadcastConnection?.cancel()
        broadcastConnection = nil
        
        let host = NWEndpoint.Host("255.255.255.255")
        let params = NWParameters.udp
        let conn = NWConnection(host: host, port: discoveryPort, using: params)
        broadcastConnection = conn
        
        conn.stateUpdateHandler = { [weak self, weak conn] state in
            if case .ready = state {
                let payload = "{\"type\":\"DISCOVER_REQUEST\"}".data(using: .utf8) ?? Data()
                conn?.send(content: payload, completion: .contentProcessed({ _ in
                    // Close broadcast connection after sending to prevent socket leaks
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                        conn?.cancel()
                        if self?.broadcastConnection === conn {
                            self?.broadcastConnection = nil
                        }
                    }
                }))
            }
        }
        
        conn.start(queue: .main)
    }
    
    // MARK: - Active Probing
    func probeKnownEndpoints() {
        let savedHost = UserDefaults.standard.string(forKey: "sync_server_host") ?? "127.0.0.1"
        let candidateEndpoints: [(String, Int)] = [
            ("127.0.0.1", 42426), // Phone via ADB forward in Simulator
            ("192.168.50.118", 42424), // Phone directly on Wi-Fi
            ("127.0.0.1", 42424), // Mac local
            ("192.168.50.96", 42424), // Mac LAN
            ("192.168.50.97", 42424)  // Windows PC
        ]
        
        for (h, p) in candidateEndpoints {
            probeHost(ip: h, port: p)
        }
    }
    
    private func probeHost(ip: String, port: Int) {
        guard let url = URL(string: "http://\(ip):\(port)/status") else { return }
        
        var request = URLRequest(url: url)
        request.timeoutInterval = 2.0
        
        URLSession.shared.dataTask(with: request) { [weak self] data, response, error in
            guard let self = self, let data = data, error == nil else { return }
            guard let status = try? JSONDecoder().decode(StatusResponse.self, from: data) else { return }
            
            DispatchQueue.main.async {
                let isPhone = port == 42426 || (status.deviceInfo != nil && status.macMedia == nil)
                
                if isPhone {
                    let pName = status.deviceInfo?.deviceName ?? "Android Telefon"
                    let pModel = status.deviceInfo?.model ?? "2409BRN2CA"
                    let pBatt = status.deviceInfo?.batteryLevel ?? 52
                    let pCharging = status.deviceInfo?.isCharging ?? false
                    let pin = status.pairingPin ?? "170260"
                    let isPaired = status.isPaired ?? true
                    
                    let phoneTarget = DiscoveredTarget(
                        id: "phone_\(ip)_\(port)",
                        name: pName,
                        model: pModel,
                        host: ip,
                        port: port,
                        type: .phone,
                        batteryLevel: pBatt,
                        isCharging: pCharging,
                        isPaired: isPaired,
                        pairingPin: pin
                    )
                    
                    if let idx = self.discoveredTargets.firstIndex(where: { $0.type == .phone }) {
                        self.discoveredTargets[idx] = phoneTarget
                    } else {
                        self.discoveredTargets.insert(phoneTarget, at: 0)
                    }
                    
                    self.activePairingPin = pin
                    self.isPaired = isPaired
                } else {
                    let isPC = ip == "192.168.50.97"
                    let compName = isPC ? "DESKTOP-VBB5GUA (Windows Sync)" : "ferit-MacBook-Pro-2.local (Mac Sync)"
                    let compTarget = DiscoveredTarget(
                        id: "comp_\(ip)_\(port)",
                        name: compName,
                        model: isPC ? "Windows PC" : "MacBook Pro",
                        host: ip,
                        port: port,
                        type: isPC ? .pc : .mac
                    )
                    
                    if !self.discoveredTargets.contains(where: { $0.id == compTarget.id }) {
                        self.discoveredTargets.append(compTarget)
                    }
                }
            }
        }.resume()
    }
}
