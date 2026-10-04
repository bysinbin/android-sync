import AppKit
import Foundation
import Network
import UserNotifications
import AVFoundation

// MARK: - Media Remote Framework Definition
typealias MRMediaRemoteSendCommandFunc = @convention(c) (Int32, AnyObject?) -> Bool
typealias MRMediaRemoteGetNowPlayingInfoFunc = @convention(c) (DispatchQueue, @escaping @convention(block) ([String: Any]?) -> Void) -> Void
typealias MRMediaRemoteRegisterFunc = @convention(c) (DispatchQueue) -> Void
typealias MRMediaRemoteSetElapsedTimeFunc = @convention(c) (Double) -> Void

struct MediaState {
    var title: String = ""
    var artist: String = ""
    var isPlaying: Bool = false
    var positionSec: Double = 0.0
    var durationSec: Double = 0.0
    var percent: Double = 0.0
}

let kMRPlay: Int32 = 0
let kMRPause: Int32 = 1
let kMRTogglePlayPause: Int32 = 2
let kMRStop: Int32 = 3
let kMRNextTrack: Int32 = 4
let kMRPreviousTrack: Int32 = 5

let NX_KEYTYPE_SOUND_UP: Int32 = 0
let NX_KEYTYPE_SOUND_DOWN: Int32 = 1
let NX_KEYTYPE_MUTE: Int32 = 7
let NX_KEYTYPE_PLAY: Int32 = 16
let NX_KEYTYPE_NEXT: Int32 = 17
let NX_KEYTYPE_PREVIOUS: Int32 = 18
let NX_KEYTYPE_FAST: Int32 = 19
let NX_KEYTYPE_REWIND: Int32 = 20

// MARK: - Direct macOS System Media Controller
class MediaManager {
    static let shared = MediaManager()
    
    private var sendCommandFunc: MRMediaRemoteSendCommandFunc?
    private var getNowPlayingFunc: MRMediaRemoteGetNowPlayingInfoFunc?
    private var setElapsedTimeFunc: MRMediaRemoteSetElapsedTimeFunc?
    var currentMedia: MediaState?
    
    init() {
        let handle = dlopen("/System/Library/PrivateFrameworks/MediaRemote.framework/MediaRemote", RTLD_NOW)
        if let regPtr = dlsym(handle, "MRMediaRemoteRegisterForNowPlayingNotifications") {
            let reg = unsafeBitCast(regPtr, to: MRMediaRemoteRegisterFunc.self)
            reg(DispatchQueue.main)
        }
        if let ptr = dlsym(handle, "MRMediaRemoteSendCommand") {
            self.sendCommandFunc = unsafeBitCast(ptr, to: MRMediaRemoteSendCommandFunc.self)
        }
        if let ptr2 = dlsym(handle, "MRMediaRemoteGetNowPlayingInfo") {
            self.getNowPlayingFunc = unsafeBitCast(ptr2, to: MRMediaRemoteGetNowPlayingInfoFunc.self)
        }
        if let ptr3 = dlsym(handle, "MRMediaRemoteSetElapsedTime") {
            self.setElapsedTimeFunc = unsafeBitCast(ptr3, to: MRMediaRemoteSetElapsedTimeFunc.self)
        }
    }
    
    func sendHardwareKey(key: Int32) {
        func doKey(down: Bool) {
            let flags = NSEvent.ModifierFlags(rawValue: down ? 0xa00 : 0xb00)
            let data1 = Int((key << 16) | (down ? 0xa00 : 0xb00))
            let ev = NSEvent.otherEvent(
                with: .systemDefined,
                location: .zero,
                modifierFlags: flags,
                timestamp: 0,
                windowNumber: 0,
                context: nil,
                subtype: 8,
                data1: data1,
                data2: -1
            )
            ev?.cgEvent?.post(tap: .cghidEventTap)
            ev?.cgEvent?.post(tap: .cgSessionEventTap)
        }
        doKey(down: true)
        usleep(15000)
        doKey(down: false)
    }
    
    func playPause() {
        if AXIsProcessTrusted() {
            sendHardwareKey(key: NX_KEYTYPE_PLAY)
        } else {
            _ = sendCommandFunc?(kMRTogglePlayPause, nil)
            sendHardwareKey(key: NX_KEYTYPE_PLAY)
        }
    }
    
    func play() {
        _ = sendCommandFunc?(kMRPlay, nil)
        sendHardwareKey(key: NX_KEYTYPE_PLAY)
    }
    
    func pause() {
        _ = sendCommandFunc?(kMRPause, nil)
        sendHardwareKey(key: NX_KEYTYPE_PLAY)
    }
    
    func next() {
        _ = sendCommandFunc?(kMRNextTrack, nil)
        sendHardwareKey(key: NX_KEYTYPE_NEXT)
    }
    
    func previous() {
        _ = sendCommandFunc?(kMRPreviousTrack, nil)
        sendHardwareKey(key: NX_KEYTYPE_PREVIOUS)
    }

    func seekForward(seconds: Double = 15.0) {
        sendHardwareKey(key: NX_KEYTYPE_FAST)
        let script = """
        try
            tell application "Spotify" to if player state is playing then set player position to (player position + \(seconds))
        end try
        try
            tell application "Music" to if player state is playing then set player position to (player position + \(seconds))
        end try
        try
            tell application "Google Chrome"
                repeat with w in windows
                    repeat with t in tabs of w
                        execute t javascript "var v=document.querySelector('video')||document.querySelector('audio'); if(v) v.currentTime+=\(seconds);"
                    end repeat
                end repeat
            end tell
        end try
        try
            tell application "Safari"
                repeat with w in windows
                    repeat with t in tabs of w
                        do JavaScript "var v=document.querySelector('video')||document.querySelector('audio'); if(v) v.currentTime+=\(seconds);" in t
                    end repeat
                end repeat
            end tell
        end try
        """
        var err: NSDictionary?
        NSAppleScript(source: script)?.executeAndReturnError(&err)
    }

    func seekBackward(seconds: Double = 15.0) {
        sendHardwareKey(key: NX_KEYTYPE_REWIND)
        let script = """
        try
            tell application "Spotify" to if player state is playing then set player position to (player position - \(seconds))
        end try
        try
            tell application "Music" to if player state is playing then set player position to (player position - \(seconds))
        end try
        try
            tell application "Google Chrome"
                repeat with w in windows
                    repeat with t in tabs of w
                        execute t javascript "var v=document.querySelector('video')||document.querySelector('audio'); if(v) v.currentTime-=\(seconds);"
                    end repeat
                end repeat
            end tell
        end try
        try
            tell application "Safari"
                repeat with w in windows
                    repeat with t in tabs of w
                        do JavaScript "var v=document.querySelector('video')||document.querySelector('audio'); if(v) v.currentTime-=\(seconds);" in t
                    end repeat
                end repeat
            end tell
        end try
        """
        var err: NSDictionary?
        NSAppleScript(source: script)?.executeAndReturnError(&err)
    }
    
    func volumeUp() {
        sendHardwareKey(key: NX_KEYTYPE_SOUND_UP)
        let script = "set volume output volume ((output volume of (get volume settings)) + 6)"
        var error: NSDictionary?
        NSAppleScript(source: script)?.executeAndReturnError(&error)
    }
    
    func volumeDown() {
        sendHardwareKey(key: NX_KEYTYPE_SOUND_DOWN)
        let script = "set volume output volume ((output volume of (get volume settings)) - 6)"
        var error: NSDictionary?
        NSAppleScript(source: script)?.executeAndReturnError(&error)
    }
    
    func mute() {
        sendHardwareKey(key: NX_KEYTYPE_MUTE)
    }

    func seekPercent(_ percent: Double) {
        let p = max(0.0, min(100.0, percent))
        if let dur = currentMedia?.durationSec, dur > 0 {
            let targetSec = dur * (p / 100.0)
            setElapsedTimeFunc?(targetSec)
        }
        let script = """
        try
            tell application "Spotify"
                if player state is playing then
                    set dur to (duration of current track) / 1000
                    if dur > 0 then set player position to (dur * \(p) / 100)
                end if
            end tell
        end try
        try
            tell application "Music"
                if player state is playing then
                    set dur to duration of current track
                    if dur > 0 then set player position to (dur * \(p) / 100)
                end if
            end tell
        end try
        try
            tell application "Google Chrome"
                repeat with w in windows
                    repeat with t in tabs of w
                        try
                            execute t javascript "var v=document.querySelector('video')||document.querySelector('audio'); if(v && v.duration) v.currentTime = v.duration * (\(p) / 100);"
                        end try
                    end repeat
                end repeat
            end tell
        end try
        try
            tell application "Safari"
                repeat with w in windows
                    repeat with t in tabs of w
                        try
                            do JavaScript "var v=document.querySelector('video')||document.querySelector('audio'); if(v && v.duration) v.currentTime = v.duration * (\(p) / 100);" in t
                        end try
                    end repeat
                end repeat
            end tell
        end try
        """
        var err: NSDictionary?
        NSAppleScript(source: script)?.executeAndReturnError(&err)
    }

    private let mediaQueue = DispatchQueue(label: "com.sync.media", qos: .userInitiated)

    func fetchNowPlaying(completion: @escaping (MediaState?) -> Void) {
        guard let getNowPlaying = self.getNowPlayingFunc else {
            completion(nil)
            return
        }
        getNowPlaying(self.mediaQueue) { [weak self] info in
            guard let info = info else {
                DispatchQueue.main.async { completion(nil) }
                return
            }
            var title = info["kMRMediaRemoteNowPlayingInfoTitle"] as? String ?? ""
            var artist = info["kMRMediaRemoteNowPlayingInfoArtist"] as? String ?? ""
            let duration = info["kMRMediaRemoteNowPlayingInfoDuration"] as? Double ?? 0
            let elapsed = info["kMRMediaRemoteNowPlayingInfoElapsedTime"] as? Double ?? 0
            let rate = info["kMRMediaRemoteNowPlayingInfoPlaybackRate"] as? Double ?? 0
            let timestamp = (info["kMRMediaRemoteNowPlayingInfoTimestamp"] as? Date)?.timeIntervalSince1970 ?? 0
            
            var currentPos = elapsed
            if rate > 0 && timestamp > 0 {
                let diff = Date().timeIntervalSince1970 - timestamp
                currentPos = min(duration, elapsed + diff * rate)
            }
            let percent = duration > 0 ? (currentPos / duration) * 100.0 : 0.0
            let isPlaying = rate > 0

            // Fallback to Spotify / Music if title is empty
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
                    if parts.count >= 2 {
                        title = parts[0]
                        artist = parts[1]
                    }
                }
            }

            let state = MediaState(
                title: title,
                artist: artist,
                isPlaying: isPlaying,
                positionSec: max(0, currentPos),
                durationSec: max(0, duration),
                percent: max(0.0, min(100.0, percent))
            )
            DispatchQueue.main.async {
                self?.currentMedia = state
                completion(state)
            }
        }
    }
    
    func execute(action: String, percent: Double? = nil) {
        if action.uppercased().starts(with: "SEEK_PERCENT") {
            if let p = percent {
                seekPercent(p)
            }
            return
        }
        switch action.uppercased() {
        case "PLAY_PAUSE", "TOGGLE":
            playPause()
        case "PLAY":
            play()
        case "PAUSE":
            pause()
        case "NEXT":
            next()
        case "PREVIOUS", "PREV":
            previous()
        case "SEEK_FORWARD", "FORWARD", "FORWARD_15":
            seekForward()
        case "SEEK_BACKWARD", "REWIND", "REWIND_15":
            seekBackward()
        case "VOLUME_UP", "VOLUP":
            volumeUp()
        case "VOLUME_DOWN", "VOLDOWN":
            volumeDown()
        case "MUTE":
            mute()
        default:
            playPause()
        }
    }
}

// MARK: - Local IPC Command Server (Port 42426)
class LocalCommandServer {
    private var listener: NWListener?
    
    func start(port: UInt16 = 42426) {
        do {
            let params = NWParameters.tcp
            self.listener = try NWListener(using: params, on: NWEndpoint.Port(rawValue: port)!)
            self.listener?.newConnectionHandler = { connection in
                connection.start(queue: .main)
                connection.receive(minimumIncompleteLength: 1, maximumLength: 1024) { data, _, isComplete, _ in
                    if let data = data, let req = String(data: data, encoding: .utf8) {
                        if let firstLine = req.components(separatedBy: "\r\n").first {
                            if firstLine.contains("/call") {
                                var state = "RINGING"
                                var caller = "Bilinmeyen"
                                if let stateRange = firstLine.range(of: "state=") {
                                    let q = String(firstLine[stateRange.upperBound...])
                                    state = q.components(separatedBy: " ").first?.components(separatedBy: "&").first?.removingPercentEncoding ?? "RINGING"
                                }
                                if let callerRange = firstLine.range(of: "caller=") {
                                    let q = String(firstLine[callerRange.upperBound...])
                                    caller = q.components(separatedBy: " ").first?.components(separatedBy: "&").first?.removingPercentEncoding ?? "Bilinmeyen"
                                }
                                DispatchQueue.main.async {
                                    if state == "RINGING" {
                                        TouchBarController.shared.showIncomingCall(caller: caller)
                                        AppDelegate.shared?.showCallNotification(caller: caller)
                                    } else {
                                        TouchBarController.shared.dismissCallTouchBar()
                                    }
                                }
                            } else if firstLine.contains("/sms") {
                                var sender = "Bilinmeyen"
                                var body = ""
                                if let senderRange = firstLine.range(of: "sender=") {
                                    let q = String(firstLine[senderRange.upperBound...])
                                    sender = q.components(separatedBy: " ").first?.components(separatedBy: "&").first?.removingPercentEncoding ?? "Bilinmeyen"
                                }
                                if let bodyRange = firstLine.range(of: "body=") {
                                    let q = String(firstLine[bodyRange.upperBound...])
                                    body = q.components(separatedBy: " ").first?.components(separatedBy: "&").first?.removingPercentEncoding ?? ""
                                }
                                DispatchQueue.main.async {
                                    AppDelegate.shared?.showSmsNotification(sender: sender, body: body)
                                }
                            } else if let actionRange = firstLine.range(of: "action=") {
                                let queryPart = String(firstLine[actionRange.upperBound...])
                                let rawAction = queryPart.components(separatedBy: " ").first?.components(separatedBy: "&").first ?? ""
                                var percentVal: Double? = nil
                                if let percentRange = firstLine.range(of: "percent=") {
                                    let pPart = String(firstLine[percentRange.upperBound...])
                                    let pRaw = pPart.components(separatedBy: " ").first?.components(separatedBy: "&").first ?? ""
                                    percentVal = Double(pRaw)
                                }
                                if let action = rawAction.removingPercentEncoding, !action.isEmpty {
                                    DispatchQueue.main.async {
                                        MediaManager.shared.execute(action: action, percent: percentVal)
                                        if let p = percentVal {
                                            TouchBarController.shared.updateMacProgress(p)
                                        }
                                    }
                                }
                            } else if firstLine.contains("/debug_media") {
                                let handle = dlopen("/System/Library/PrivateFrameworks/MediaRemote.framework/MediaRemote", RTLD_NOW)
                                let ptr = dlsym(handle, "MRMediaRemoteGetNowPlayingInfo")
                                var debugOut = "ptr: \(ptr != nil)\n"
                                if let ptr = ptr {
                                    let fn = unsafeBitCast(ptr, to: MRMediaRemoteGetNowPlayingInfoFunc.self)
                                    let sema = DispatchSemaphore(value: 0)
                                    fn(DispatchQueue.global()) { info in
                                        debugOut += "info is nil: \(info == nil)\n"
                                        if let info = info {
                                            for (k, v) in info {
                                                debugOut += "\(k): \(v)\n"
                                            }
                                        }
                                        sema.signal()
                                    }
                                    _ = sema.wait(timeout: .now() + 2)
                                }
                                let response = "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: \(debugOut.utf8.count)\r\nConnection: close\r\n\r\n\(debugOut)"
                                connection.send(content: response.data(using: .utf8), completion: .contentProcessed({ _ in
                                    connection.cancel()
                                }))
                                return
                            } else if firstLine.contains("/current_media") {
                                let m = MediaManager.shared.currentMedia
                                var jsonStr = "{}"
                                if let m = m, !m.title.isEmpty {
                                    let cleanTitle = m.title.replacingOccurrences(of: "\"", with: "\\\"")
                                    let cleanArtist = m.artist.replacingOccurrences(of: "\"", with: "\\\"")
                                    jsonStr = "{\"title\":\"\(cleanTitle)\",\"artist\":\"\(cleanArtist)\",\"is_playing\":\(m.isPlaying),\"percent\":\(m.percent),\"position_ms\":\(Int64(m.positionSec * 1000)),\"duration_ms\":\(Int64(m.durationSec * 1000))}"
                                }
                                let response = "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: \(jsonStr.utf8.count)\r\nConnection: close\r\n\r\n\(jsonStr)"
                                connection.send(content: response.data(using: .utf8), completion: .contentProcessed({ _ in
                                    connection.cancel()
                                }))
                                return
                            }
                        }
                        let response = "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nOK"
                        connection.send(content: response.data(using: .utf8), completion: .contentProcessed({ _ in
                            connection.cancel()
                        }))
                    }
                }
            }
            self.listener?.start(queue: .main)
            print("[MacSync IPC] Yerel medya komut sunucusu port \(port)'ta hazır.")
        } catch {
            print("[MacSync IPC] Sunucu başlatılamadı: \(error)")
        }
    }
}

// MARK: - MacBook Pro Touch Bar Controller
class TouchBarController: NSObject, NSTouchBarDelegate {
    static let shared = TouchBarController()
    static let stripIdentifier = NSTouchBarItem.Identifier("com.sync.mac.controlstrip")
    
    var stripItem: NSCustomTouchBarItem?
    var modalTouchBar: NSTouchBar?
    var isCallActive: Bool = false
    var currentCaller: String = ""
    
    func setupControlStrip() {
        let item = NSCustomTouchBarItem(identifier: TouchBarController.stripIdentifier)
        let btn = NSButton(title: "📱 MacSync", target: self, action: #selector(onStripTapped))
        if #available(macOS 11.0, *), let img = NSImage(systemSymbolName: "iphone.radiowaves.left.and.right", accessibilityDescription: nil) {
            btn.image = img
            btn.imagePosition = .imageLeading
        }
        item.view = btn
        self.stripItem = item
        
        let sel = NSSelectorFromString("addSystemTrayItem:")
        if NSTouchBarItem.responds(to: sel) {
            NSTouchBarItem.perform(sel, with: item)
        }
    }
    
    func updateStatus(info: String) {
        if let btn = stripItem?.view as? NSButton {
            btn.title = "📱 \(info)"
        }
    }
    
    @objc func onStripTapped() {
        let tb = makeTouchBar()
        self.modalTouchBar = tb
        let sel = NSSelectorFromString("presentSystemModalTouchBar:systemTrayItemIdentifier:")
        if NSTouchBar.responds(to: sel) {
            NSTouchBar.perform(sel, with: tb, with: TouchBarController.stripIdentifier)
        }
    }
    
    func showIncomingCall(caller: String) {
        self.isCallActive = true
        self.currentCaller = caller
        let tb = NSTouchBar()
        tb.delegate = self
        tb.defaultItemIdentifiers = [
            NSTouchBarItem.Identifier("tb.call.caller"),
            NSTouchBarItem.Identifier("tb.call.answer"),
            NSTouchBarItem.Identifier("tb.call.reject"),
            NSTouchBarItem.Identifier("tb.call.mute"),
            NSTouchBarItem.Identifier("tb.close")
        ]
        self.modalTouchBar = tb
        let sel = NSSelectorFromString("presentSystemModalTouchBar:systemTrayItemIdentifier:")
        if NSTouchBar.responds(to: sel) {
            NSTouchBar.perform(sel, with: tb, with: TouchBarController.stripIdentifier)
        }
    }

    func dismissCallTouchBar() {
        if isCallActive {
            isCallActive = false
            dismissTouchBar()
        }
    }

    @objc func dismissTouchBar() {
        if let tb = modalTouchBar {
            let sel = NSSelectorFromString("dismissSystemModalTouchBar:")
            if NSTouchBar.responds(to: sel) {
                NSTouchBar.perform(sel, with: tb)
            }
        }
    }
    
    var currentMacProgress: Double = 0.0
    var currentPhoneProgress: Double = 0.0
    var currentMacTitle: String = ""
    var currentPhoneTitle: String = ""
    var macTitleItem: NSCustomTouchBarItem?
    var macSliderItem: NSSliderTouchBarItem?
    var phoneSliderItem: NSSliderTouchBarItem?

    func updateMacProgress(_ percent: Double) {
        self.currentMacProgress = percent
        macSliderItem?.slider.doubleValue = percent
    }

    func updateMacMedia(state: MediaState) {
        self.currentMacProgress = state.percent
        self.currentMacTitle = state.title.isEmpty ? "" : "🎵 " + state.title
        if let tf = macTitleItem?.view as? NSTextField {
            tf.stringValue = self.currentMacTitle.isEmpty ? "🎵 Mac" : self.currentMacTitle
        }
        macSliderItem?.slider.doubleValue = state.percent
        let macLabel = state.title.isEmpty ? "⏱" : "⏱ " + (state.title.count > 12 ? String(state.title.prefix(10)) + "..." : state.title)
        macSliderItem?.label = macLabel
    }

    func updatePhoneMedia(title: String, artist: String, percent: Double) {
        self.currentPhoneProgress = percent
        self.currentPhoneTitle = title
        let phoneLabel = title.isEmpty ? "📱 ⏱" : "📱 " + (title.count > 12 ? String(title.prefix(10)) + "..." : title)
        phoneSliderItem?.label = phoneLabel
        phoneSliderItem?.slider.doubleValue = percent
    }

    func makeTouchBar() -> NSTouchBar {
        let tb = NSTouchBar()
        tb.delegate = self
        tb.defaultItemIdentifiers = [
            NSTouchBarItem.Identifier("tb.status"),
            NSTouchBarItem.Identifier("tb.mac.title"),
            NSTouchBarItem.Identifier("tb.mac.slider"),
            NSTouchBarItem.Identifier("tb.mac.playpause"),
            NSTouchBarItem.Identifier("tb.mac.rewind"),
            NSTouchBarItem.Identifier("tb.mac.forward"),
            NSTouchBarItem.Identifier("tb.phone.slider"),
            NSTouchBarItem.Identifier("tb.phone.playpause"),
            NSTouchBarItem.Identifier("tb.phone.ring"),
            NSTouchBarItem.Identifier("tb.close")
        ]
        return tb
    }
    
    func touchBar(_ touchBar: NSTouchBar, makeItemForIdentifier identifier: NSTouchBarItem.Identifier) -> NSTouchBarItem? {
        switch identifier.rawValue {
        case "tb.mac.title":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let tf = NSTextField(labelWithString: currentMacTitle.isEmpty ? "🎵 Mac" : currentMacTitle)
            tf.font = NSFont.systemFont(ofSize: 11, weight: .bold)
            tf.textColor = NSColor.systemBlue
            tf.lineBreakMode = .byTruncatingTail
            tf.preferredMaxLayoutWidth = 130
            item.view = tf
            self.macTitleItem = item
            return item
        case "tb.mac.slider":
            let sliderItem = NSSliderTouchBarItem(identifier: identifier)
            sliderItem.label = "⏱"
            sliderItem.slider.minValue = 0.0
            sliderItem.slider.maxValue = 100.0
            sliderItem.slider.doubleValue = currentMacProgress
            sliderItem.target = self
            sliderItem.action = #selector(onMacSliderMoved(_:))
            self.macSliderItem = sliderItem
            return sliderItem
        case "tb.phone.slider":
            let sliderItem = NSSliderTouchBarItem(identifier: identifier)
            let phoneLabel = currentPhoneTitle.isEmpty ? "📱 ⏱" : "📱 " + (currentPhoneTitle.count > 12 ? String(currentPhoneTitle.prefix(10)) + "..." : currentPhoneTitle)
            sliderItem.label = phoneLabel
            sliderItem.slider.minValue = 0.0
            sliderItem.slider.maxValue = 100.0
            sliderItem.slider.doubleValue = currentPhoneProgress
            sliderItem.target = self
            sliderItem.action = #selector(onPhoneSliderMoved(_:))
            self.phoneSliderItem = sliderItem
            return sliderItem
        case "tb.call.caller":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let tf = NSTextField(labelWithString: "📞 ARAYAN: \(currentCaller)")
            tf.font = NSFont.boldSystemFont(ofSize: 13)
            tf.textColor = NSColor.systemRed
            item.view = tf
            return item
        case "tb.call.answer":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let btn = NSButton(title: "📞 Yanıtla", target: self, action: #selector(onCallAnswer))
            btn.bezelColor = NSColor.systemGreen
            item.view = btn
            return item
        case "tb.call.reject":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let btn = NSButton(title: "✕ Reddet", target: self, action: #selector(onCallReject))
            btn.bezelColor = NSColor.systemRed
            item.view = btn
            return item
        case "tb.call.mute":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let btn = NSButton(title: "🔕 Sessize Al", target: self, action: #selector(onPhoneMute))
            btn.bezelColor = NSColor.systemOrange
            item.view = btn
            return item
        case "tb.status":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let info = (NSApp.delegate as? AppDelegate)?.connectedInfo ?? "MacSync"
            let tf = NSTextField(labelWithString: "📱 \(info)")
            tf.font = NSFont.boldSystemFont(ofSize: 12)
            item.view = tf
            return item
        case "tb.mac.rewind":
            let item = NSCustomTouchBarItem(identifier: identifier)
            item.view = NSButton(title: "⏪ 15s", target: self, action: #selector(onMacRewind))
            return item
        case "tb.mac.playpause":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let btn = NSButton(title: "⏯ Mac", target: self, action: #selector(onMacPlayPause))
            btn.bezelColor = NSColor.systemBlue
            item.view = btn
            return item
        case "tb.mac.forward":
            let item = NSCustomTouchBarItem(identifier: identifier)
            item.view = NSButton(title: "⏩ 15s", target: self, action: #selector(onMacForward))
            return item
        case "tb.mac.next":
            let item = NSCustomTouchBarItem(identifier: identifier)
            item.view = NSButton(title: "⏭ Mac", target: self, action: #selector(onMacNext))
            return item
        case "tb.phone.rewind":
            let item = NSCustomTouchBarItem(identifier: identifier)
            item.view = NSButton(title: "📱⏪", target: self, action: #selector(onPhoneRewind))
            return item
        case "tb.phone.playpause":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let btn = NSButton(title: "📱⏯", target: self, action: #selector(onPhonePlayPause))
            btn.bezelColor = NSColor.systemPurple
            item.view = btn
            return item
        case "tb.phone.forward":
            let item = NSCustomTouchBarItem(identifier: identifier)
            item.view = NSButton(title: "📱⏩", target: self, action: #selector(onPhoneForward))
            return item
        case "tb.phone.ring":
            let item = NSCustomTouchBarItem(identifier: identifier)
            let btn = NSButton(title: "🔔 Bul", target: self, action: #selector(onPhoneRing))
            btn.bezelColor = NSColor.systemRed
            item.view = btn
            return item
        case "tb.close":
            let item = NSCustomTouchBarItem(identifier: identifier)
            item.view = NSButton(title: "✖", target: self, action: #selector(dismissTouchBar))
            return item
        default:
            return nil
        }
    }
    
    @objc func onMacSliderMoved(_ sender: NSSliderTouchBarItem) {
        let val = sender.slider.doubleValue
        currentMacProgress = val
        MediaManager.shared.seekPercent(val)
    }

    @objc func onPhoneSliderMoved(_ sender: NSSliderTouchBarItem) {
        let val = sender.slider.doubleValue
        currentPhoneProgress = val
        (NSApp.delegate as? AppDelegate)?.sendPhoneCommand("SEEK_PERCENT&percent=\(String(format: "%.1f", val))")
    }

    @objc func onMacPlayPause() { MediaManager.shared.playPause() }
    @objc func onMacForward() { MediaManager.shared.seekForward() }
    @objc func onMacRewind() { MediaManager.shared.seekBackward() }
    @objc func onMacNext() { MediaManager.shared.next() }
    
    @objc func onPhonePlayPause() { (NSApp.delegate as? AppDelegate)?.sendPhoneCommand("PLAY_PAUSE") }
    @objc func onPhoneForward() { (NSApp.delegate as? AppDelegate)?.sendPhoneCommand("FORWARD_15") }
    @objc func onPhoneRewind() { (NSApp.delegate as? AppDelegate)?.sendPhoneCommand("REWIND_15") }
    @objc func onPhoneMute() { (NSApp.delegate as? AppDelegate)?.sendPhoneCommand("MUTE") }
    @objc func onPhoneRing() { (NSApp.delegate as? AppDelegate)?.sendPhoneCommand("RING") }

    @objc func onCallAnswer() {
        dismissCallTouchBar()
        if let url = URL(string: "http://127.0.0.1:42424/call/action?action=ANSWER") {
            var req = URLRequest(url: url)
            req.timeoutInterval = 1.0
            URLSession.shared.dataTask(with: req).resume()
        }
    }

    @objc func onCallReject() {
        dismissCallTouchBar()
        if let url = URL(string: "http://127.0.0.1:42424/call/action?action=REJECT") {
            var req = URLRequest(url: url)
            req.timeoutInterval = 1.0
            URLSession.shared.dataTask(with: req).resume()
        }
    }
}

// MARK: - App Delegate & Menu Bar Manager
class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate, UNUserNotificationCenterDelegate {
    static var shared: AppDelegate?
    
    var statusItem: NSStatusItem!
    var daemonProcess: Process?
    var timer: Timer?
    let commandServer = LocalCommandServer()
    
    var isPhoneConnected: Bool = false
    var connectedInfo: String = "Bağlantı Aranıyor..."
    
    var phoneMediaTitle: String = ""
    var phoneMediaArtist: String = ""
    var phoneMediaPosSec: Double = 0.0
    var phoneMediaDurSec: Double = 0.0
    var phoneMediaPercent: Double = 0.0

    func formatTimeSec(_ sec: Double) -> String {
        let s = Int(max(0, sec))
        return String(format: "%02d:%02d", s / 60, s % 60)
    }
    
    func applicationDidFinishLaunching(_ notification: Notification) {
        AppDelegate.shared = self
        if #available(macOS 10.14, *) {
            UNUserNotificationCenter.current().delegate = self
        }
        setupStatusItem()
        commandServer.start(port: 42426)
        TouchBarController.shared.setupControlStrip()
        startDaemon()
        startStatusTimer()
        
        // Auto-check accessibility without intrusive alert at startup
        _ = AXIsProcessTrusted()
    }
    
    func showCallNotification(caller: String) {
        if #available(macOS 10.14, *) {
            let content = UNMutableNotificationContent()
            content.title = "📞 Telefon Çalıyor"
            content.subtitle = "Gelen Arama..."
            content.body = caller.isEmpty ? "Bilinmeyen Numara" : caller
            content.sound = UNNotificationSound.default
            let req = UNNotificationRequest(identifier: "incoming_call_\(Date().timeIntervalSince1970)", content: content, trigger: nil)
            UNUserNotificationCenter.current().add(req, withCompletionHandler: nil)
        }
    }

    func showSmsNotification(sender: String, body: String) {
        if #available(macOS 10.14, *) {
            let content = UNMutableNotificationContent()
            content.title = "💬 Yeni Mesaj: \(sender)"
            content.body = body
            content.sound = UNNotificationSound.default
            let req = UNNotificationRequest(identifier: "incoming_sms_\(Date().timeIntervalSince1970)", content: content, trigger: nil)
            UNUserNotificationCenter.current().add(req, withCompletionHandler: nil)
        }
    }
    
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        if #available(macOS 11.0, *) {
            completionHandler([.banner, .sound, .badge, .list])
        } else {
            completionHandler([.alert, .sound, .badge])
        }
    }
    
    func setupStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        
        if let button = statusItem.button {
            if #available(macOS 11.0, *), let img = NSImage(systemSymbolName: "iphone.radiowaves.left.and.right", accessibilityDescription: "Mac Sync") {
                img.isTemplate = true
                button.image = img
            } else {
                button.title = "📱"
            }
            button.toolTip = "Mac Sync - Android Entegrasyonu"
        }
        
        buildMenu()
    }
    
    func menuWillOpen(_ menu: NSMenu) {
        // Refresh menu dynamically when user clicks the icon
        buildMenu()
    }
    
    func buildMenu() {
        let menu = NSMenu()
        menu.delegate = self
        
        // --- 1. BAĞLANTI DURUMU ---
        let statusTitle = isPhoneConnected ? "🟢 Android Bağlandı (\(connectedInfo))" : "🟡 Android Bağlantısı Bekleniyor..."
        let statusItemHeader = NSMenuItem(title: statusTitle, action: nil, keyEquivalent: "")
        statusItemHeader.isEnabled = false
        menu.addItem(statusItemHeader)
        
        let macIpItem = NSMenuItem(title: "📍 Mac Portu: 42424 (Web & Pano)", action: nil, keyEquivalent: "")
        macIpItem.isEnabled = false
        menu.addItem(macIpItem)
        menu.addItem(NSMenuItem.separator())
        
        // --- 2. MEDYA KONTROLLERİ ---
        let mediaHeader = NSMenuItem(title: "🎵 MAC MEDYA KONTROLÜ (TÜM SİSTEM)", action: nil, keyEquivalent: "")
        mediaHeader.isEnabled = false
        menu.addItem(mediaHeader)
        
        // Show current Mac track title if available
        if let m = MediaManager.shared.currentMedia, !m.title.isEmpty {
            let titleStr = m.artist.isEmpty ? m.title : "\(m.title) - \(m.artist)"
            let trTitle = titleStr.count > 34 ? String(titleStr.prefix(32)) + "..." : titleStr
            let tItem = NSMenuItem(title: "🎵 \(trTitle)", action: nil, keyEquivalent: "")
            tItem.isEnabled = false
            menu.addItem(tItem)
        }
        
        // Interactive Mac Media Slider
        let macSliderContainer = NSView(frame: NSRect(x: 0, y: 0, width: 260, height: 44))
        var macSliderText = "⏱ Mac Süresi: %\(Int(TouchBarController.shared.currentMacProgress))"
        var macSliderVal = TouchBarController.shared.currentMacProgress
        if let m = MediaManager.shared.currentMedia, !m.title.isEmpty {
            macSliderText = "⏱ Mac: \(formatTimeSec(m.positionSec)) / \(formatTimeSec(m.durationSec)) (%\(Int(m.percent)))"
            macSliderVal = m.percent
        }
        let macSliderLabel = NSTextField(labelWithString: macSliderText)
        macSliderLabel.frame = NSRect(x: 18, y: 24, width: 224, height: 16)
        macSliderLabel.font = NSFont.systemFont(ofSize: 11, weight: .bold)
        macSliderLabel.textColor = NSColor.secondaryLabelColor
        macSliderContainer.addSubview(macSliderLabel)

        let macSlider = NSSlider(value: macSliderVal, minValue: 0, maxValue: 100, target: self, action: #selector(onMacMenuSliderChanged(_:)))
        macSlider.frame = NSRect(x: 18, y: 4, width: 224, height: 20)
        macSlider.isContinuous = true
        macSliderContainer.addSubview(macSlider)

        let macSliderItem = NSMenuItem()
        macSliderItem.view = macSliderContainer
        menu.addItem(macSliderItem)

        menu.addItem(NSMenuItem(title: "⏯  Oynat / Duraklat", action: #selector(onPlayPause), keyEquivalent: " "))
        menu.addItem(NSMenuItem(title: "⏩  İleri Sar (+15s)", action: #selector(onMacForward), keyEquivalent: "f"))
        menu.addItem(NSMenuItem(title: "⏪  Geri Sar (-15s)", action: #selector(onMacRewind), keyEquivalent: "r"))
        menu.addItem(NSMenuItem(title: "⏭  Sonraki Parça / Video", action: #selector(onNext), keyEquivalent: "n"))
        menu.addItem(NSMenuItem(title: "⏮  Önceki Parça / Video", action: #selector(onPrev), keyEquivalent: "b"))
        menu.addItem(NSMenuItem(title: "🔊  Sesi Aç (+)", action: #selector(onVolUp), keyEquivalent: "+"))
        menu.addItem(NSMenuItem(title: "🔉  Sesi Kıs (-)", action: #selector(onVolDown), keyEquivalent: "-"))
        menu.addItem(NSMenuItem(title: "🔇  Sesi Kapat / Aç", action: #selector(onMute), keyEquivalent: "m"))
        menu.addItem(NSMenuItem.separator())
        
        // --- 2B. TELEFON KONTROLÜ (ANDROID) ---
        let phoneHeader = NSMenuItem(title: "📱 TELEFON KONTROLÜ (ANDROID)", action: nil, keyEquivalent: "")
        phoneHeader.isEnabled = false
        menu.addItem(phoneHeader)
        
        // Show current Phone track title if available
        if !self.phoneMediaTitle.isEmpty {
            let titleStr = self.phoneMediaArtist.isEmpty ? self.phoneMediaTitle : "\(self.phoneMediaTitle) - \(self.phoneMediaArtist)"
            let trTitle = titleStr.count > 34 ? String(titleStr.prefix(32)) + "..." : titleStr
            let tItem = NSMenuItem(title: "📱 \(trTitle)", action: nil, keyEquivalent: "")
            tItem.isEnabled = false
            menu.addItem(tItem)
        }
        
        // Interactive Phone Media Slider
        let phoneSliderContainer = NSView(frame: NSRect(x: 0, y: 0, width: 260, height: 44))
        var phoneSliderText = "📱 Telefon Süresi: %\(Int(TouchBarController.shared.currentPhoneProgress))"
        var phoneSliderVal = TouchBarController.shared.currentPhoneProgress
        if !self.phoneMediaTitle.isEmpty {
            phoneSliderText = "⏱ Telefon: \(formatTimeSec(self.phoneMediaPosSec)) / \(formatTimeSec(self.phoneMediaDurSec)) (%\(Int(self.phoneMediaPercent)))"
            phoneSliderVal = self.phoneMediaPercent
        }
        let phoneSliderLabel = NSTextField(labelWithString: phoneSliderText)
        phoneSliderLabel.frame = NSRect(x: 18, y: 24, width: 224, height: 16)
        phoneSliderLabel.font = NSFont.systemFont(ofSize: 11, weight: .bold)
        phoneSliderLabel.textColor = NSColor.secondaryLabelColor
        phoneSliderContainer.addSubview(phoneSliderLabel)

        let phoneSlider = NSSlider(value: phoneSliderVal, minValue: 0, maxValue: 100, target: self, action: #selector(onPhoneMenuSliderChanged(_:)))
        phoneSlider.frame = NSRect(x: 18, y: 4, width: 224, height: 20)
        phoneSlider.isContinuous = true
        phoneSlider.isEnabled = isPhoneConnected
        phoneSliderContainer.addSubview(phoneSlider)

        let phoneSliderItem = NSMenuItem()
        phoneSliderItem.view = phoneSliderContainer
        menu.addItem(phoneSliderItem)

        let pPlayPause = NSMenuItem(title: "⏯  Telefonda Oynat / Duraklat", action: #selector(onPhonePlayPause), keyEquivalent: "")
        let pForward = NSMenuItem(title: "⏩  Telefonda İleri Sar (+15s)", action: #selector(onPhoneForward), keyEquivalent: "")
        let pRewind = NSMenuItem(title: "⏪  Telefonda Geri Sar (-15s)", action: #selector(onPhoneRewind), keyEquivalent: "")
        let pNext = NSMenuItem(title: "⏭  Telefonda Sonraki Parça", action: #selector(onPhoneNext), keyEquivalent: "")
        let pPrev = NSMenuItem(title: "⏮  Telefonda Önceki Parça", action: #selector(onPhonePrev), keyEquivalent: "")
        let pVolUp = NSMenuItem(title: "🔊  Telefon Sesini Aç (+)", action: #selector(onPhoneVolUp), keyEquivalent: "")
        let pVolDown = NSMenuItem(title: "🔉  Telefon Sesini Kıs (-)", action: #selector(onPhoneVolDown), keyEquivalent: "")
        let pMute = NSMenuItem(title: "🔇  Telefonu Sessize Al", action: #selector(onPhoneMute), keyEquivalent: "")
        let pRing = NSMenuItem(title: "🔔  Telefonumu Çaldır (Alarm)", action: #selector(onPhoneRing), keyEquivalent: "")
        let pStopRing = NSMenuItem(title: "🔕  Telefon Alarmını Sustur", action: #selector(onPhoneStopRing), keyEquivalent: "")
        
        pPlayPause.isEnabled = isPhoneConnected
        pForward.isEnabled = isPhoneConnected
        pRewind.isEnabled = isPhoneConnected
        pNext.isEnabled = isPhoneConnected
        pPrev.isEnabled = isPhoneConnected
        pVolUp.isEnabled = isPhoneConnected
        pVolDown.isEnabled = isPhoneConnected
        pMute.isEnabled = isPhoneConnected
        pRing.isEnabled = isPhoneConnected
        pStopRing.isEnabled = isPhoneConnected
        
        menu.addItem(pPlayPause)
        menu.addItem(pForward)
        menu.addItem(pRewind)
        menu.addItem(pNext)
        menu.addItem(pPrev)
        menu.addItem(pVolUp)
        menu.addItem(pVolDown)
        menu.addItem(pMute)
        menu.addItem(pRing)
        menu.addItem(pStopRing)
        menu.addItem(NSMenuItem.separator())
        
        // --- 2C. SES AKTARIMI (MAC ⇄ TELEFON) ---
        let audioHeader = NSMenuItem(title: "🔊 SES AKTARIMI (MAC ⇄ TELEFON)", action: nil, keyEquivalent: "")
        audioHeader.isEnabled = false
        menu.addItem(audioHeader)
        
        menu.addItem(NSMenuItem(title: "📶  Bluetooth ile Sıfır Gecikmeli Ses Bağla...", action: #selector(openBluetoothSettings), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "🎧  Wi-Fi Ses Yayını (http://...:42424/audio/stream)", action: #selector(openAudioStreamInfo), keyEquivalent: ""))
        menu.addItem(NSMenuItem.separator())
        
        // --- 3. YETKİLER VE İZİNLER ---
        let permHeader = NSMenuItem(title: "⚙️ YETKİLER VE İZİNLER", action: nil, keyEquivalent: "")
        permHeader.isEnabled = false
        menu.addItem(permHeader)
        
        let isTrusted = AXIsProcessTrusted()
        if isTrusted {
            let axItem = NSMenuItem(title: "✅ Erişilebilirlik İzni: Verildi", action: #selector(openAccessibility), keyEquivalent: "")
            menu.addItem(axItem)
        } else {
            let axItem = NSMenuItem(title: "⚠️ Erişilebilirlik İzni Ver (Gerekli) ➔", action: #selector(grantAccessibility), keyEquivalent: "")
            axItem.attributedTitle = NSAttributedString(
                string: "⚠️ Erişilebilirlik İzni Ver (Gerekli) ➔",
                attributes: [.foregroundColor: NSColor.systemOrange]
            )
            menu.addItem(axItem)
        }
        
        menu.addItem(NSMenuItem(title: "🔔  Bildirim İzinlerini Doğrula ➔", action: #selector(requestNotificationPermission), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "⚡  Otomasyon İzinlerini Aç ➔", action: #selector(openAutomation), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "🛡️  Tüm İzin Pencerelerini Aç ➔", action: #selector(requestAllPermissions), keyEquivalent: ""))
        menu.addItem(NSMenuItem.separator())
        
        // --- 4. HIZLI ERİŞİM & YÖNETİM ---
        menu.addItem(NSMenuItem(title: "📲  Android APK'yı Aç / İndir...", action: #selector(openApkDownload), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "📜  Canlı Servis Günlüğünü Aç (Log)...", action: #selector(openLogFile), keyEquivalent: "l"))
        menu.addItem(NSMenuItem(title: "📂  MacSync Proje Klasörünü Aç...", action: #selector(openProjectDir), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "🔄  Mac Servisini Yeniden Başlat", action: #selector(restartDaemon), keyEquivalent: "r"))
        menu.addItem(NSMenuItem(title: "🛑  MacSync'den Çıkış Yap", action: #selector(quitApp), keyEquivalent: "q"))
        
        statusItem.menu = menu
    }
    
    func startDaemon() {
        stopDaemon()
        
        var daemonPath: String?
        let candidates = [
            Bundle.main.path(forResource: "mac-sync", ofType: nil),
            Bundle.main.bundlePath + "/Contents/Resources/mac-sync",
            "/Users/feritetem/Desktop/android-mac-sync/mac-daemon/mac-sync",
            "/Users/feritetem/Desktop/android-mac-sync/MacSync.app/Contents/Resources/mac-sync"
        ]
        
        for c in candidates {
            if let p = c, FileManager.default.fileExists(atPath: p) {
                daemonPath = p
                break
            }
        }
        
        guard let exe = daemonPath else {
            print("[MacSync] mac-sync daemon bulunamadı!")
            return
        }
        
        let process = Process()
        process.executableURL = URL(fileURLWithPath: exe)
        process.currentDirectoryURL = URL(fileURLWithPath: "/Users/feritetem/Desktop/android-mac-sync")
        
        let logPath = "/Users/feritetem/Desktop/android-mac-sync/mac-sync.log"
        if !FileManager.default.fileExists(atPath: logPath) {
            FileManager.default.createFile(atPath: logPath, contents: nil)
        }
        if let handle = FileHandle(forWritingAtPath: logPath) {
            handle.seekToEndOfFile()
            process.standardOutput = handle
            process.standardError = handle
        }
        
        do {
            try process.run()
            daemonProcess = process
            print("[MacSync] Go daemon başlatıldı (PID: \(process.processIdentifier))")
        } catch {
            print("[MacSync] Daemon başlatma hatası: \(error)")
        }
    }
    
    func stopDaemon() {
        if let p = daemonProcess, p.isRunning {
            p.terminate()
            p.waitUntilExit()
            daemonProcess = nil
        }
    }
    
    func startStatusTimer() {
        checkDaemonStatus()
        syncMacMedia()
        let t = Timer(timeInterval: 1.5, repeats: true) { [weak self] _ in
            self?.checkDaemonStatus()
            self?.syncMacMedia()
        }
        RunLoop.main.add(t, forMode: .common)
        self.timer = t
    }

    func syncMacMedia() {
        MediaManager.shared.fetchNowPlaying { state in
            guard let state = state else { return }
            TouchBarController.shared.updateMacMedia(state: state)
            
            if !state.title.isEmpty, let url = URL(string: "http://127.0.0.1:42424/media/mac_update") {
                var req = URLRequest(url: url)
                req.httpMethod = "POST"
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
                let payload: [String: Any] = [
                    "title": state.title,
                    "artist": state.artist,
                    "is_playing": state.isPlaying,
                    "position_ms": Int64(state.positionSec * 1000),
                    "duration_ms": Int64(state.durationSec * 1000),
                    "percent": state.percent
                ]
                req.httpBody = try? JSONSerialization.data(withJSONObject: payload)
                URLSession.shared.dataTask(with: req) { _, _, _ in }.resume()
            }
        }
    }
    
    func checkDaemonStatus() {
        guard let url = URL(string: "http://127.0.0.1:42424/status") else { return }
        URLSession.shared.dataTask(with: url) { [weak self] data, response, error in
            DispatchQueue.main.async {
                guard let self = self else { return }
                if let data = data {
                    if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                        let hasClients = json["connected"] as? Bool ?? ((json["clients_count"] as? Int ?? 0) > 0)
                        self.isPhoneConnected = hasClients
                        if hasClients {
                            let model = json["model"] as? String ?? "Android"
                            let battery = json["battery_level"] as? Int ?? -1
                            let isCharging = json["is_charging"] as? Bool ?? false
                            var info = model
                            if battery >= 0 {
                                info += " (Pil: %\(battery)\(isCharging ? " ⚡" : ""))"
                            }
                            self.connectedInfo = info
                        } else {
                            self.connectedInfo = "Bekleniyor"
                        }

                        // Mac Media parsing from Daemon
                        if let mm = json["mac_media"] as? [String: Any] {
                            let title = mm["title"] as? String ?? ""
                            let artist = mm["artist"] as? String ?? ""
                            let percent = mm["percent"] as? Double ?? 0.0
                            let posMs = mm["position_ms"] as? Int64 ?? (mm["position_ms"] as? Double != nil ? Int64(mm["position_ms"] as! Double) : 0)
                            let durMs = mm["duration_ms"] as? Int64 ?? (mm["duration_ms"] as? Double != nil ? Int64(mm["duration_ms"] as! Double) : 0)
                            let isPlaying = mm["is_playing"] as? Bool ?? false
                            let state = MediaState(
                                title: title,
                                artist: artist,
                                isPlaying: isPlaying,
                                positionSec: Double(posMs) / 1000.0,
                                durationSec: Double(durMs) / 1000.0,
                                percent: percent
                            )
                            MediaManager.shared.currentMedia = state
                            TouchBarController.shared.updateMacMedia(state: state)
                        }

                        // Phone Media parsing
                        if let pm = json["phone_media"] as? [String: Any] {
                            self.phoneMediaTitle = pm["title"] as? String ?? ""
                            self.phoneMediaArtist = pm["artist"] as? String ?? ""
                            self.phoneMediaPercent = pm["percent"] as? Double ?? 0.0
                            let posMs = pm["position_ms"] as? Int64 ?? (pm["position_ms"] as? Double != nil ? Int64(pm["position_ms"] as! Double) : 0)
                            let durMs = pm["duration_ms"] as? Int64 ?? (pm["duration_ms"] as? Double != nil ? Int64(pm["duration_ms"] as! Double) : 0)
                            self.phoneMediaPosSec = Double(posMs) / 1000.0
                            self.phoneMediaDurSec = Double(durMs) / 1000.0
                            TouchBarController.shared.updatePhoneMedia(
                                title: self.phoneMediaTitle,
                                artist: self.phoneMediaArtist,
                                percent: self.phoneMediaPercent
                            )
                        }
                    } else if let text = String(data: data, encoding: .utf8), text.contains("clients: ") {
                        let hasClients = !text.contains("clients: 0")
                        self.isPhoneConnected = hasClients
                        self.connectedInfo = hasClients ? "Aktif" : "Bekleniyor"
                    }
                    
                    if let button = self.statusItem.button {
                        if #available(macOS 11.0, *) {
                            let iconName = self.isPhoneConnected ? "iphone.radiowaves.left.and.right" : "iphone"
                            button.image = NSImage(systemSymbolName: iconName, accessibilityDescription: "Mac Sync")
                        }
                    }
                    
                    TouchBarController.shared.updateStatus(info: self.connectedInfo)
                } else {
                    self.isPhoneConnected = false
                    self.connectedInfo = "Durduruldu"
                }
            }
        }.resume()
    }
    
    // MARK: - Mac Media Actions
    @objc func onMacMenuSliderChanged(_ sender: NSSlider) {
        let val = sender.doubleValue
        TouchBarController.shared.updateMacProgress(val)
        MediaManager.shared.seekPercent(val)
    }

    @objc func onPhoneMenuSliderChanged(_ sender: NSSlider) {
        let val = sender.doubleValue
        TouchBarController.shared.currentPhoneProgress = val
        sendPhoneCommand("SEEK_PERCENT&percent=\(String(format: "%.1f", val))")
    }

    @objc func onPlayPause() { MediaManager.shared.playPause() }
    @objc func onNext() { MediaManager.shared.next() }
    @objc func onPrev() { MediaManager.shared.previous() }
    @objc func onMacForward() { MediaManager.shared.seekForward() }
    @objc func onMacRewind() { MediaManager.shared.seekBackward() }
    @objc func onVolUp() { MediaManager.shared.volumeUp() }
    @objc func onVolDown() { MediaManager.shared.volumeDown() }
    @objc func onMute() { MediaManager.shared.mute() }
    
    // MARK: - Phone Actions (Mac to Android)
    @objc func onPhonePlayPause() { sendPhoneCommand("PLAY_PAUSE") }
    @objc func onPhoneForward() { sendPhoneCommand("FORWARD_15") }
    @objc func onPhoneRewind() { sendPhoneCommand("REWIND_15") }
    @objc func onPhoneNext() { sendPhoneCommand("NEXT") }
    @objc func onPhonePrev() { sendPhoneCommand("PREVIOUS") }
    @objc func onPhoneVolUp() { sendPhoneCommand("VOLUME_UP") }
    @objc func onPhoneVolDown() { sendPhoneCommand("VOLUME_DOWN") }
    @objc func onPhoneMute() { sendPhoneCommand("MUTE") }
    @objc func onPhoneRing() { sendPhoneCommand("RING") }
    @objc func onPhoneStopRing() { sendPhoneCommand("STOP_RING") }
    
    func sendPhoneCommand(_ action: String) {
        guard let url = URL(string: "http://127.0.0.1:42424/phone/command?action=\(action)") else { return }
        URLSession.shared.dataTask(with: url).resume()
    }
    
    // MARK: - Audio Actions
    @objc func openBluetoothSettings() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.Bluetooth") {
            NSWorkspace.shared.open(url)
        }
    }
    
    @objc func openAudioStreamInfo() {
        if let url = URL(string: "http://127.0.0.1:42424/audio/stream") {
            NSWorkspace.shared.open(url)
        }
    }
    
    // MARK: - Permission Handlers
    @objc func grantAccessibility() {
        let promptOption = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(promptOption)
        openAccessibility()
    }
    
    @objc func openAccessibility() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility") {
            NSWorkspace.shared.open(url)
        }
    }
    
    @objc func requestNotificationPermission() {
        if #available(macOS 10.14, *) {
            UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, error in
                DispatchQueue.main.async {
                    if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Notifications") {
                        NSWorkspace.shared.open(url)
                    }
                }
            }
        } else {
            if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Notifications") {
                NSWorkspace.shared.open(url)
            }
        }
    }
    
    @objc func openAutomation() {
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation") {
            NSWorkspace.shared.open(url)
        }
    }
    
    @objc func requestAllPermissions() {
        grantAccessibility()
        requestNotificationPermission()
    }
    
    // MARK: - Helper Actions
    @objc func openApkDownload() {
        let apkPath = "/Users/feritetem/Desktop/android-mac-sync/MacSync.apk"
        if FileManager.default.fileExists(atPath: apkPath) {
            NSWorkspace.shared.selectFile(apkPath, inFileViewerRootedAtPath: "/Users/feritetem/Desktop/android-mac-sync")
        } else if let url = URL(string: "http://127.0.0.1:42424/download") {
            NSWorkspace.shared.open(url)
        }
    }
    
    @objc func openLogFile() {
        let logPath = "/Users/feritetem/Desktop/android-mac-sync/mac-sync.log"
        NSWorkspace.shared.open(URL(fileURLWithPath: logPath))
    }
    
    @objc func openProjectDir() {
        NSWorkspace.shared.open(URL(fileURLWithPath: "/Users/feritetem/Desktop/android-mac-sync"))
    }
    
    @objc func restartDaemon() {
        startDaemon()
        buildMenu()
    }
    
    @objc func quitApp() {
        stopDaemon()
        NSApplication.shared.terminate(nil)
    }
    
    func applicationWillTerminate(_ notification: Notification) {
        stopDaemon()
    }
}

// MARK: - Entry Point
let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = AppDelegate()
app.delegate = delegate
app.run()
