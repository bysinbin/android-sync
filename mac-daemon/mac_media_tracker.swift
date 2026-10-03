import Darwin
import Foundation
import AppKit

// MARK: - Media Remote Framework Types
typealias MRMediaRemoteGetNowPlayingInfoFunc = @convention(c) (DispatchQueue, @escaping @convention(block) ([String: Any]?) -> Void) -> Void
typealias MRMediaRemoteRegisterFunc = @convention(c) (DispatchQueue) -> Void
typealias MRMediaRemoteSetElapsedTimeFunc = @convention(c) (Double) -> Void

let handle = dlopen("/System/Library/PrivateFrameworks/MediaRemote.framework/MediaRemote", RTLD_NOW)
if let regPtr = dlsym(handle, "MRMediaRemoteRegisterForNowPlayingNotifications") {
    let reg = unsafeBitCast(regPtr, to: MRMediaRemoteRegisterFunc.self)
    reg(DispatchQueue.main)
}

guard let getPtr = dlsym(handle, "MRMediaRemoteGetNowPlayingInfo") else {
    fputs("Error: MRMediaRemoteGetNowPlayingInfo not found\n", stderr)
    exit(1)
}
let getNowPlaying = unsafeBitCast(getPtr, to: MRMediaRemoteGetNowPlayingInfoFunc.self)

var lastTitle = ""
var lastArtist = ""
var lastPosMs: Int64 = -1
var lastDurMs: Int64 = -1
var lastPercent: Double = -1.0
var lastPlaying: Bool = false

func queryMedia() {
    getNowPlaying(DispatchQueue.global()) { info in
        var title = info?["kMRMediaRemoteNowPlayingInfoTitle"] as? String ?? ""
        var artist = info?["kMRMediaRemoteNowPlayingInfoArtist"] as? String ?? ""
        var album = info?["kMRMediaRemoteNowPlayingInfoAlbum"] as? String ?? ""
        var duration = info?["kMRMediaRemoteNowPlayingInfoDuration"] as? Double ?? 0
        let elapsed = info?["kMRMediaRemoteNowPlayingInfoElapsedTime"] as? Double ?? 0
        let rate = info?["kMRMediaRemoteNowPlayingInfoPlaybackRate"] as? Double ?? 0
        let timestamp = (info?["kMRMediaRemoteNowPlayingInfoTimestamp"] as? Date)?.timeIntervalSince1970 ?? 0

        var currentPos = elapsed
        if rate > 0 && timestamp > 0 {
            let diff = Date().timeIntervalSince1970 - timestamp
            currentPos = min(duration, elapsed + diff * rate)
        }
        var percent = duration > 0 ? (currentPos / duration) * 100.0 : 0.0
        var isPlaying = rate > 0

        // Fallback to Spotify & Apple Music if title is empty
        if title.isEmpty {
            let s = """
            try
                tell application "Spotify" to if player state is playing then return (name of current track & "||" & artist of current track & "||" & (player position as string) & "||" & ((duration of current track)/1000 as string))
            end try
            try
                tell application "Music" to if player state is playing then return (name of current track & "||" & artist of current track & "||" & (player position as string) & "||" & (duration of current track as string))
            end try
            return ""
            """
            var err: NSDictionary?
            if let desc = NSAppleScript(source: s)?.executeAndReturnError(&err).stringValue, !desc.isEmpty {
                let parts = desc.components(separatedBy: "||")
                if parts.count >= 4 {
                    title = parts[0]
                    artist = parts[1]
                    if let pos = Double(parts[2]), let dur = Double(parts[3]), dur > 0 {
                        currentPos = pos
                        duration = dur
                        percent = (pos / dur) * 100.0
                        isPlaying = true
                    }
                }
            }
        }

        let posMs = Int64(currentPos * 1000)
        let durMs = Int64(duration * 1000)

        // Clean strings for JSON
        let cleanTitle = title.replacingOccurrences(of: "\"", with: "\\\"").trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanArtist = artist.replacingOccurrences(of: "\"", with: "\\\"").trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanAlbum = album.replacingOccurrences(of: "\"", with: "\\\"").trimmingCharacters(in: .whitespacesAndNewlines)

        let json = "{\"source\":\"mac\",\"title\":\"\(cleanTitle)\",\"artist\":\"\(cleanArtist)\",\"album\":\"\(cleanAlbum)\",\"is_playing\":\(isPlaying),\"position_ms\":\(posMs),\"duration_ms\":\(durMs),\"percent\":\(percent)}"

        // Output to stdout for Go Daemon scanner
        print(json)
        fflush(stdout)

        // Also POST to HTTP daemon endpoint if changed
        let titleChanged = (cleanTitle != lastTitle || cleanArtist != lastArtist)
        let playStateChanged = (isPlaying != lastPlaying)
        let posMoved = abs(percent - lastPercent) >= 0.5

        if titleChanged || playStateChanged || posMoved {
            lastTitle = cleanTitle
            lastArtist = cleanArtist
            lastPosMs = posMs
            lastDurMs = durMs
            lastPercent = percent
            lastPlaying = isPlaying

            if let url = URL(string: "http://127.0.0.1:42424/media/mac_update") {
                var req = URLRequest(url: url)
                req.httpMethod = "POST"
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                req.httpBody = json.data(using: .utf8)
                let task = URLSession.shared.dataTask(with: req) { _, _, _ in }
                task.resume()
            }
        }
    }
}

// Immediate initial query
queryMedia()

// Timer loop every 1.0 second on CFRunLoop
let timer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { _ in
    queryMedia()
}

// Run main event loop with CoreFoundation RunLoop
CFRunLoopRun()

