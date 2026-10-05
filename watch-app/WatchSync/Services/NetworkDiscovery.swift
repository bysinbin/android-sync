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
    
    private var listener: NWListener?
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
        discoveredTargets.removeAll()
        discoveredServers.removeAll()
        
        startListener()
        sendBroadcastRequest()
        probeKnownEndpoints()
        
        // Periyodik olarak 3 sn de bir probe yenile
        probeTimer?.invalidate()
        probeTimer = Timer.scheduledTimer(withTimeInterval: 3.0, repeats: true) { [weak self] _ in
            self?.probeKnownEndpoints()
            self?.sendBroadcastRequest()
        }
    }
    
    func stopDiscovery() {
        isSearching = false
        searchStatus = "Arama durduruldu"
        probeTimer?.invalidate()
        probeTimer = nil
        listener?.cancel()
        listener = nil
        broadcastConnection?.cancel()
        broadcastConnection = nil
    }
    
    private func startListener() {
        do {
            let params = NWParameters.udp
            params.allowLocalEndpointReuse = true
            listener = try NWListener(using: params, on: discoveryPort)
            
            listener?.newConnectionHandler = { [weak self] connection in
                self?.handleIncomingConnection(connection)
            }
            
            listener?.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    print("📡 UDP Discovery listener hazır: \(self.discoveryPort)")
                case .failed(let err):
                    print("⚠️ UDP Discovery listener hatası: \(err)")
                default:
                    break
                }
            }
            
            listener?.start(queue: .main)
        } catch {
            print("⚠️ Listener başlatılamadı: \(error)")
        }
    }
    
    private func handleIncomingConnection(_ connection: NWConnection) {
        connection.start(queue: .main)
        connection.receiveMessage { [weak self] content, _, _, _ in
            guard let self = self, let content = content else { return }
            self.parseDiscoveryData(content, from: connection.endpoint)
        }
    }
    
    private func sendBroadcastRequest() {
        let host = NWEndpoint.Host("255.255.255.255")
        let params = NWParameters.udp
        broadcastConnection = NWConnection(host: host, port: discoveryPort, using: params)
        
        broadcastConnection?.stateUpdateHandler = { [weak self] state in
            if case .ready = state {
                let payload = "{\"type\":\"DISCOVER_REQUEST\"}".data(using: .utf8) ?? Data()
                self?.broadcastConnection?.send(content: payload, completion: .contentProcessed({ error in
                    if let err = error {
                        print("Broadcast gönderim hatası: \(err)")
                    }
                }))
            }
        }
        
        broadcastConnection?.start(queue: .main)
    }
    
    private func parseDiscoveryData(_ data: Data, from endpoint: NWEndpoint) {
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = json["type"] as? String,
              (type == "DISCOVER_BEACON" || type == "DISCOVER_RESPONSE"),
              let serverName = json["server_name"] as? String,
              let wsPort = json["ws_port"] as? Int else {
            return
        }
        
        var ip = ""
        if case .hostPort(let host, _) = endpoint {
            switch host {
            case .ipv4(let addr):
                ip = addr.debugDescription
            case .name(let name, _):
                ip = name
            default:
                break
            }
        }
        
        if ip.isEmpty {
            ip = "127.0.0.1"
        }
        
        // Add Computer Server
        let server = DiscoveredServer(name: serverName, host: ip, port: wsPort)
        let isPC = serverName.uppercased().contains("DESKTOP") || serverName.uppercased().contains("WINDOWS")
        let compTarget = DiscoveredTarget(
            id: "comp_\(ip)_\(wsPort)",
            name: serverName,
            model: isPC ? "Windows PC" : "MacBook Pro",
            host: ip,
            port: wsPort,
            type: isPC ? .pc : .mac
        )
        
        DispatchQueue.main.async {
            if !self.discoveredServers.contains(where: { $0.host == server.host && $0.port == server.port }) {
                self.discoveredServers.append(server)
            }
            if !self.discoveredTargets.contains(where: { $0.id == compTarget.id }) {
                self.discoveredTargets.append(compTarget)
            }
            
            // Eğer pakette telefon bilgisi varsa hemen ekle
            if let phoneName = json["phone_name"] as? String, !phoneName.isEmpty {
                let phoneModel = json["phone_model"] as? String ?? "Android"
                let phoneIp = json["phone_ip"] as? String ?? ip
                let phoneBatt = json["phone_battery"] as? Int ?? 100
                let phoneCharging = json["phone_charging"] as? Bool ?? false
                let pin = json["pairing_pin"] as? String
                let isPaired = json["is_paired"] as? Bool ?? false
                
                let phoneTarget = DiscoveredTarget(
                    id: "phone_\(phoneIp)_\(phoneModel)",
                    name: phoneName,
                    model: phoneModel,
                    host: phoneIp,
                    port: wsPort,
                    type: .phone,
                    batteryLevel: phoneBatt,
                    isCharging: phoneCharging,
                    isPaired: isPaired,
                    pairingPin: pin
                )
                
                if let idx = self.discoveredTargets.firstIndex(where: { $0.id == phoneTarget.id }) {
                    self.discoveredTargets[idx] = phoneTarget
                } else {
                    self.discoveredTargets.insert(phoneTarget, at: 0)
                }
                
                if let pin = pin, !pin.isEmpty {
                    self.activePairingPin = pin
                    self.isPaired = isPaired
                }
            }
        }
        
        // Ayrıca arka planda /status query yapıp tam cihaz listesini al
        probeHost(ip: ip, port: wsPort)
    }
    
    // MARK: - Active Probing
    func probeKnownEndpoints() {
        let savedHost = UserDefaults.standard.string(forKey: "sync_server_host") ?? "127.0.0.1"
        let savedPort = UserDefaults.standard.integer(forKey: "sync_server_port")
        let port = savedPort > 0 ? savedPort : 42424
        
        let candidateHosts = Array(Set(["127.0.0.1", "192.168.50.96", "192.168.50.97", "192.168.50.118", savedHost]))
        for h in candidateHosts {
            probeHost(ip: h, port: port)
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
                let compName = status.localIp == "192.168.50.97" ? "Windows PC" : "Mac (Ferit)"
                let isPC = compName.contains("Windows")
                let compTarget = DiscoveredTarget(
                    id: "comp_\(ip)_\(port)",
                    name: compName,
                    model: isPC ? "Windows Sync" : "macOS Sync",
                    host: ip,
                    port: port,
                    type: isPC ? .pc : .mac,
                    isPaired: status.isPaired ?? false,
                    pairingPin: status.pairingPin
                )
                
                if !self.discoveredTargets.contains(where: { $0.id == compTarget.id }) {
                    self.discoveredTargets.append(compTarget)
                }
                
                if let pin = status.pairingPin, !pin.isEmpty {
                    if !isPC || self.activePairingPin.isEmpty {
                        self.activePairingPin = pin
                        self.isPaired = status.isPaired ?? false
                    }
                }
                
                // 1. devices arrayindeki telefonlar
                if let devs = status.devices, !devs.isEmpty {
                    for d in devs {
                        if d.ip == "127.0.0.1" && devs.count > 1 { continue }
                        let phoneTarget = DiscoveredTarget(
                            id: "phone_\(d.ip)_\(d.model)",
                            name: d.name.isEmpty ? "Android Telefon" : d.name,
                            model: d.model.isEmpty ? "2409BRN2CA" : d.model,
                            host: d.ip,
                            port: port,
                            type: .phone,
                            batteryLevel: d.batteryLevel,
                            isCharging: d.isCharging,
                            isPaired: d.isPaired ?? status.isPaired ?? false,
                            pairingPin: status.pairingPin
                        )
                        
                        if let idx = self.discoveredTargets.firstIndex(where: { $0.id == phoneTarget.id }) {
                            self.discoveredTargets[idx] = phoneTarget
                        } else {
                            self.discoveredTargets.insert(phoneTarget, at: 0)
                        }
                    }
                } else if let dev = status.deviceInfo {
                    // 2. tekli device_info
                    let phoneTarget = DiscoveredTarget(
                        id: "phone_info_\(dev.model)",
                        name: dev.deviceName.isEmpty ? "Android Telefon" : dev.deviceName,
                        model: dev.model,
                        host: ip,
                        port: port,
                        type: .phone,
                        batteryLevel: dev.batteryLevel,
                        isCharging: dev.isCharging,
                        isPaired: status.isPaired ?? false,
                        pairingPin: status.pairingPin
                    )
                    if let idx = self.discoveredTargets.firstIndex(where: { $0.id == phoneTarget.id }) {
                        self.discoveredTargets[idx] = phoneTarget
                    } else {
                        self.discoveredTargets.insert(phoneTarget, at: 0)
                    }
                }
                
                self.searchStatus = "\(self.discoveredPhones.count) Telefon, \(self.discoveredComputers.count) Bilgisayar bulundu"
            }
        }.resume()
    }
}
