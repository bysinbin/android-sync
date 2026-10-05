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
    static let shared = NetworkDiscovery()
    
    @Published var discoveredTargets: [DiscoveredTarget] = []
    @Published var discoveredServers: [DiscoveredServer] = []
    @Published var activePairingPin: String = ""
    @Published var isPaired: Bool = false
    @Published var isSearching: Bool = false
    @Published var searchStatus: String = "Hazır"
    @Published var localIP: String? = nil
    
    private var broadcastConnection: NWConnection?
    private var bonjourBrowser: NWBrowser?
    private let discoveryPort: NWEndpoint.Port = 42425
    private var probeTimer: Timer?
    
    var discoveredPhones: [DiscoveredTarget] {
        discoveredTargets.filter { $0.type == .phone }
    }
    
    var discoveredComputers: [DiscoveredTarget] {
        discoveredTargets.filter { $0.type != .phone }
    }
    
    init() {
        self.localIP = Self.getLocalIP()
    }
    
    func startDiscovery() {
        guard !isSearching else { return }
        isSearching = true
        searchStatus = "Cihazlar taranıyor..."
        self.localIP = Self.getLocalIP()
        
        probeKnownEndpoints()
        sendBroadcastRequest()
        startBonjourBrowser()
        
        probeTimer?.invalidate()
        probeTimer = Timer.scheduledTimer(withTimeInterval: 3.5, repeats: true) { [weak self] _ in
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
        bonjourBrowser?.cancel()
        bonjourBrowser = nil
    }
    
    // MARK: - Bonjour / mDNS Discovery
    private func startBonjourBrowser() {
        bonjourBrowser?.cancel()
        let desc = NWBrowser.Descriptor.bonjour(type: "_macsync._tcp", domain: "local.")
        let params = NWParameters()
        let browser = NWBrowser(for: desc, using: params)
        self.bonjourBrowser = browser
        
        browser.browseResultsChangedHandler = { [weak self] results, changes in
            guard let self = self else { return }
            for result in results {
                if case .service(let name, _, _, _) = result.endpoint {
                    print("📡 [NetworkDiscovery] Bonjour Servis Bulundu: \(name)")
                    // Resolve service host
                    self.resolveBonjourEndpoint(result.endpoint)
                }
            }
        }
        
        browser.start(queue: .main)
    }
    
    private func resolveBonjourEndpoint(_ endpoint: NWEndpoint) {
        let conn = NWConnection(to: endpoint, using: .tcp)
        conn.stateUpdateHandler = { [weak self, weak conn] state in
            if case .ready = state {
                if let remote = conn?.currentPath?.remoteEndpoint,
                   case .hostPort(let host, let port) = remote {
                    let hostStr = "\(host)".replacingOccurrences(of: "%en0", with: "")
                    let portVal = Int(port.rawValue)
                    print("✅ [NetworkDiscovery] Bonjour Çözüldü: \(hostStr):\(portVal)")
                    self?.probeHost(ip: hostStr, port: portVal)
                }
                conn?.cancel()
            }
        }
        conn.start(queue: .main)
    }
    
    // MARK: - UDP Broadcast Request
    private func sendBroadcastRequest() {
        broadcastConnection?.cancel()
        broadcastConnection = nil
        
        let host = NWEndpoint.Host("255.255.255.255")
        let params = NWParameters.udp
        let conn = NWConnection(host: host, port: discoveryPort, using: params)
        broadcastConnection = conn
        
        conn.stateUpdateHandler = { [weak conn] state in
            if case .ready = state {
                let payload = "{\"type\":\"DISCOVER_REQUEST\"}".data(using: .utf8) ?? Data()
                conn?.send(content: payload, completion: .contentProcessed({ _ in
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
                        conn?.cancel()
                    }
                }))
            }
        }
        
        conn.start(queue: .main)
    }
    
    // MARK: - Active Dynamic Probing
    func probeKnownEndpoints() {
        var candidateEndpoints: [(String, Int)] = []
        
        // 1. Saved hosts from UserDefaults
        if let savedPhone = UserDefaults.standard.string(forKey: "last_known_phone_host"), !savedPhone.isEmpty {
            candidateEndpoints.append((savedPhone, 42424))
            candidateEndpoints.append((savedPhone, 42426))
        }
        if let savedMac = UserDefaults.standard.string(forKey: "last_known_mac_host"), !savedMac.isEmpty {
            candidateEndpoints.append((savedMac, 42424))
        }
        if let savedManual = UserDefaults.standard.string(forKey: "sync_server_host"), !savedManual.isEmpty {
            candidateEndpoints.append((savedManual, 42424))
            candidateEndpoints.append((savedManual, 42426))
        }
        
        // 2. Loopback / Simulator fallback
        candidateEndpoints.append(("127.0.0.1", 42424))
        candidateEndpoints.append(("127.0.0.1", 42426))
        
        // 3. Current dynamic subnet prefix probe
        if let currentIP = Self.getLocalIP(), let prefix = Self.getSubnetPrefix(from: currentIP) {
            candidateEndpoints.append(("\(prefix)1", 42424))
            candidateEndpoints.append(("\(prefix)254", 42424))
            
            // Common local static IPs on this subnet
            for lastOctet in [2, 10, 20, 50, 96, 97, 100, 118, 120, 150, 200] {
                candidateEndpoints.append(("\(prefix)\(lastOctet)", 42424))
                candidateEndpoints.append(("\(prefix)\(lastOctet)", 42426))
            }
        }
        
        // 4. Hotspot subnet (Android / iOS standard hotspot range: 172.20.10.x, 192.168.43.x)
        candidateEndpoints.append(("172.20.10.1", 42424))
        candidateEndpoints.append(("172.20.10.1", 42426))
        candidateEndpoints.append(("192.168.43.1", 42424))
        candidateEndpoints.append(("192.168.43.1", 42426))
        
        // Deduplicate
        var seen = Set<String>()
        var uniqueEndpoints: [(String, Int)] = []
        for ep in candidateEndpoints {
            let key = "\(ep.0):\(ep.1)"
            if !seen.contains(key) {
                seen.insert(key)
                uniqueEndpoints.append(ep)
            }
        }
        
        for (h, p) in uniqueEndpoints {
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
                    let pModel = status.deviceInfo?.model ?? "Android"
                    let pBatt = status.deviceInfo?.batteryLevel ?? 50
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
                    
                    // Save last working phone host
                    UserDefaults.standard.set(ip, forKey: "last_known_phone_host")
                    
                    // Notify active sync service
                    WatchSyncService.shared.onDiscoveredPhoneHost(ip: ip, port: port)
                } else {
                    let compName = status.macMedia?.deviceName ?? "Mac / Windows Sync"
                    let compTarget = DiscoveredTarget(
                        id: "comp_\(ip)_\(port)",
                        name: compName,
                        model: "Bilgisayar",
                        host: ip,
                        port: port,
                        type: .mac
                    )
                    
                    if !self.discoveredTargets.contains(where: { $0.id == compTarget.id }) {
                        self.discoveredTargets.append(compTarget)
                    }
                    
                    UserDefaults.standard.set(ip, forKey: "last_known_mac_host")
                }
            }
        }.resume()
    }
    
    // MARK: - Local IP & Subnet Helpers
    static func getLocalIP() -> String? {
        var address: String?
        var ifaddr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddr) == 0, let firstAddr = ifaddr else { return nil }
        
        for ptr in sequence(first: firstAddr, next: { $0.pointee.ifa_next }) {
            let interface = ptr.pointee
            let addrFamily = interface.ifa_addr.pointee.sa_family
            if addrFamily == UInt8(AF_INET) {
                let name = String(cString: interface.ifa_name)
                if name == "en0" || name == "pdp_ip0" {
                    var hostname = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                    getnameinfo(interface.ifa_addr, socklen_t(interface.ifa_addr.pointee.sa_len),
                                &hostname, socklen_t(hostname.count),
                                nil, socklen_t(0), NI_NUMERICHOST)
                    address = String(cString: hostname)
                    if name == "en0" { break }
                }
            }
        }
        freeifaddrs(ifaddr)
        return address
    }
    
    static func getSubnetPrefix(from ip: String) -> String? {
        let parts = ip.split(separator: ".")
        guard parts.count == 4 else { return nil }
        return "\(parts[0]).\(parts[1]).\(parts[2])."
    }
}
