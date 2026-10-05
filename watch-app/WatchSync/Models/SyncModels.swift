import Foundation

// MARK: - Enums
enum MediaSource: String, CaseIterable, Identifiable {
    case phone = "phone"
    case mac = "mac"
    
    var id: String { rawValue }
    
    var title: String {
        switch self {
        case .phone: return "Telefon"
        case .mac: return "Mac"
        }
    }
    
    var iconName: String {
        switch self {
        case .phone: return "iphone"
        case .mac: return "laptopcomputer"
        }
    }
}

// MARK: - Generic WebSocket Envelope
struct WSMessage: Codable {
    let event: String
    let payload: AnyCodable?
    
    init(event: String, payload: AnyCodable? = nil) {
        self.event = event
        self.payload = payload
    }
}

// MARK: - Media Models
struct MediaInfo: Codable, Equatable {
    var source: String? = "phone"
    var title: String = ""
    var artist: String = ""
    var album: String = ""
    var isPlaying: Bool = false
    var positionMs: Int64 = 0
    var durationMs: Int64 = 0
    var percent: Double = 0.0
    var volume: Int? = 50
    var deviceId: String? = nil
    var deviceName: String? = nil
    var artworkUrl: String? = nil
    var artworkBase64: String? = nil
    
    enum CodingKeys: String, CodingKey {
        case source
        case title
        case artist
        case album
        case isPlaying = "is_playing"
        case positionMs = "position_ms"
        case durationMs = "duration_ms"
        case percent
        case volume
        case deviceId = "device_id"
        case deviceName = "device_name"
        case artworkUrl = "artwork_url"
        case artworkBase64 = "artwork_base64"
    }
    
    init(source: String? = "phone", title: String = "", artist: String = "", album: String = "", isPlaying: Bool = false, positionMs: Int64 = 0, durationMs: Int64 = 0, percent: Double = 0.0, artworkUrl: String? = nil, artworkBase64: String? = nil) {
        self.source = source
        self.title = title
        self.artist = artist
        self.album = album
        self.isPlaying = isPlaying
        self.positionMs = positionMs
        self.durationMs = durationMs
        self.percent = percent
        self.artworkUrl = artworkUrl
        self.artworkBase64 = artworkBase64
    }
    
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.source = try container.decodeIfPresent(String.self, forKey: .source) ?? "phone"
        self.title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
        self.artist = try container.decodeIfPresent(String.self, forKey: .artist) ?? ""
        self.album = try container.decodeIfPresent(String.self, forKey: .album) ?? ""
        self.isPlaying = try container.decodeIfPresent(Bool.self, forKey: .isPlaying) ?? false
        self.positionMs = try container.decodeIfPresent(Int64.self, forKey: .positionMs) ?? 0
        self.durationMs = try container.decodeIfPresent(Int64.self, forKey: .durationMs) ?? 0
        
        let rawPercent = try container.decodeIfPresent(Double.self, forKey: .percent) ?? 0.0
        // If percent is 0..100 from Android, normalize to 0..1
        self.percent = rawPercent > 1.0 ? (rawPercent / 100.0) : rawPercent
        
        self.volume = try container.decodeIfPresent(Int.self, forKey: .volume) ?? 50
        self.deviceId = try container.decodeIfPresent(String.self, forKey: .deviceId)
        self.deviceName = try container.decodeIfPresent(String.self, forKey: .deviceName)
        self.artworkUrl = try container.decodeIfPresent(String.self, forKey: .artworkUrl)
        self.artworkBase64 = try container.decodeIfPresent(String.self, forKey: .artworkBase64)
    }
    
    var formattedPosition: String {
        let totalSec = max(0, positionMs / 1000)
        let m = totalSec / 60
        let s = totalSec % 60
        return String(format: "%d:%02d", m, s)
    }
    
    var formattedDuration: String {
        let totalSec = max(0, durationMs / 1000)
        let m = totalSec / 60
        let s = totalSec % 60
        return String(format: "%d:%02d", m, s)
    }
}

// MARK: - Device Info Model
struct DeviceInfo: Codable, Equatable {
    var deviceName: String = "Android Telefon"
    var model: String = "Android"
    var batteryLevel: Int = 100
    var isCharging: Bool = false
    
    enum CodingKeys: String, CodingKey {
        case deviceName = "device_name"
        case model
        case batteryLevel = "battery_level"
        case isCharging = "is_charging"
    }
    
    init(deviceName: String = "Android Telefon", model: String = "Android", batteryLevel: Int = 100, isCharging: Bool = false) {
        self.deviceName = deviceName
        self.model = model
        self.batteryLevel = batteryLevel
        self.isCharging = isCharging
    }
    
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.deviceName = try container.decodeIfPresent(String.self, forKey: .deviceName) ?? "Android Telefon"
        self.model = try container.decodeIfPresent(String.self, forKey: .model) ?? "Android"
        self.batteryLevel = try container.decodeIfPresent(Int.self, forKey: .batteryLevel) ?? 100
        self.isCharging = try container.decodeIfPresent(Bool.self, forKey: .isCharging) ?? false
    }
}

// MARK: - Call State Model
struct CallState: Codable, Equatable {
    var state: String = "IDLE" // RINGING, OFFHOOK, IDLE
    var phoneNumber: String = ""
    var callerName: String = ""
    var deviceId: String? = nil
    var deviceName: String? = nil
    
    enum CodingKeys: String, CodingKey {
        case state
        case phoneNumber = "phone_number"
        case callerName = "caller_name"
        case deviceId = "device_id"
        case deviceName = "device_name"
    }
    
    init(state: String = "IDLE", phoneNumber: String = "", callerName: String = "") {
        self.state = state
        self.phoneNumber = phoneNumber
        self.callerName = callerName
    }
    
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.state = try container.decodeIfPresent(String.self, forKey: .state) ?? "IDLE"
        self.phoneNumber = try container.decodeIfPresent(String.self, forKey: .phoneNumber) ?? ""
        self.callerName = try container.decodeIfPresent(String.self, forKey: .callerName) ?? ""
        self.deviceId = try container.decodeIfPresent(String.self, forKey: .deviceId)
        self.deviceName = try container.decodeIfPresent(String.self, forKey: .deviceName)
    }
    
    var isRinging: Bool {
        return state.uppercased() == "RINGING"
    }
    
    var isOffhook: Bool {
        return state.uppercased() == "OFFHOOK"
    }
}

// MARK: - Notification Model
struct NotificationItem: Codable, Identifiable, Equatable {
    var id: String
    var packageName: String
    var appName: String
    var title: String
    var text: String
    var timestamp: Int64
    
    enum CodingKeys: String, CodingKey {
        case id
        case packageName = "package_name"
        case appName = "app_name"
        case title
        case text
        case timestamp
    }
}

// MARK: - Discovered Device Item
struct DiscoveredDeviceItem: Codable, Identifiable, Hashable {
    var id: String
    var name: String
    var model: String
    var ip: String
    var remoteAddr: String?
    var batteryLevel: Int?
    var isCharging: Bool?
    var isPaired: Bool?
    
    enum CodingKeys: String, CodingKey {
        case id
        case name
        case model
        case ip
        case remoteAddr = "remote_addr"
        case batteryLevel = "battery_level"
        case isCharging = "is_charging"
        case isPaired = "is_paired"
    }
}

// MARK: - Full Status Response from /status
struct StatusResponse: Codable {
    var status: String?
    var connected: Bool?
    var clientsCount: Int?
    var localIp: String?
    var port: Int?
    var isPaired: Bool?
    var pairedDeviceName: String?
    var pairingPin: String?
    var macMedia: MediaInfo?
    var phoneMedia: MediaInfo?
    var deviceInfo: DeviceInfo?
    var callState: CallState?
    var notifications: [NotificationItem]?
    var devices: [DiscoveredDeviceItem]?
    
    enum CodingKeys: String, CodingKey {
        case status
        case connected
        case clientsCount = "clients_count"
        case localIp = "local_ip"
        case port
        case isPaired = "is_paired"
        case pairedDeviceName = "paired_device_name"
        case pairingPin = "pairing_pin"
        case macMedia = "mac_media"
        case phoneMedia = "phone_media"
        case deviceInfo = "device_info"
        case callState = "call_state"
        case notifications
        case devices
    }
}

// MARK: - AnyCodable Helper for arbitrary JSON
struct AnyCodable: Codable {
    let value: Any
    
    init(_ value: Any) {
        self.value = value
    }
    
    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let b = try? container.decode(Bool.self) {
            value = b
        } else if let i = try? container.decode(Int.self) {
            value = i
        } else if let d = try? container.decode(Double.self) {
            value = d
        } else if let s = try? container.decode(String.self) {
            value = s
        } else if let arr = try? container.decode([AnyCodable].self) {
            value = arr.map { $0.value }
        } else if let dict = try? container.decode([String: AnyCodable].self) {
            value = dict.mapValues { $0.value }
        } else {
            value = ()
        }
    }
    
    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch value {
        case let b as Bool:
            try container.encode(b)
        case let i as Int:
            try container.encode(i)
        case let d as Double:
            try container.encode(d)
        case let s as String:
            try container.encode(s)
        case let arr as [Any]:
            try container.encode(arr.map { AnyCodable($0) })
        case let dict as [String: Any]:
            try container.encode(dict.mapValues { AnyCodable($0) })
        default:
            try container.encodeNil()
        }
    }
}
