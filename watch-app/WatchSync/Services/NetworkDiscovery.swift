import Foundation
import Network

struct DiscoveredServer: Identifiable, Hashable {
    let id = UUID()
    let name: String
    let host: String
    let port: Int
}

class NetworkDiscovery: ObservableObject {
    @Published var discoveredServers: [DiscoveredServer] = []
    @Published var isSearching: Bool = false
    
    private var listener: NWListener?
    private var broadcastConnection: NWConnection?
    private let discoveryPort: NWEndpoint.Port = 42425
    
    func startDiscovery() {
        guard !isSearching else { return }
        isSearching = true
        discoveredServers.removeAll()
        
        startListener()
        sendBroadcastRequest()
    }
    
    func stopDiscovery() {
        isSearching = false
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
        
        let server = DiscoveredServer(name: serverName, host: ip, port: wsPort)
        DispatchQueue.main.async {
            if !self.discoveredServers.contains(where: { $0.host == server.host && $0.port == server.port }) {
                self.discoveredServers.append(server)
            }
        }
    }
}
