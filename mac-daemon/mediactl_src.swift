import Foundation
import AppKit

// MediaRemote private framework
typealias MRMediaRemoteSendCommandFunc = @convention(c) (Int32, AnyObject?) -> Bool

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

func sendMediaRemote(cmd: Int32) -> Bool {
    guard let bundle = CFBundleCreate(kCFAllocatorDefault, NSURL(fileURLWithPath: "/System/Library/PrivateFrameworks/MediaRemote.framework")),
          let ptr = CFBundleGetFunctionPointerForName(bundle, "MRMediaRemoteSendCommand" as CFString) else {
        return false
    }
    let sendCommand = unsafeBitCast(ptr, to: MRMediaRemoteSendCommandFunc.self)
    return sendCommand(cmd, nil)
}

func sendHardwareKey(key: Int32) {
    func doKey(down: Bool) {
        let flags = NSEvent.ModifierFlags(rawValue: down ? 0xa00 : 0xb00)
        let data1 = Int((key << 16) | (down ? 0xa00 : 0xb00))
        let ev = NSEvent.otherEvent(
            with: .systemDefined,
            location: NSPoint.zero,
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

let action = CommandLine.arguments.count > 1 ? CommandLine.arguments[1].lowercased() : "play_pause"

switch action {
case "play_pause", "toggle":
    if AXIsProcessTrusted() {
        sendHardwareKey(key: NX_KEYTYPE_PLAY)
    } else {
        _ = sendMediaRemote(cmd: kMRTogglePlayPause)
        sendHardwareKey(key: NX_KEYTYPE_PLAY)
    }
    print("Action play_pause executed")

case "play":
    _ = sendMediaRemote(cmd: kMRPlay)
    sendHardwareKey(key: NX_KEYTYPE_PLAY)
    print("Action play executed")

case "pause":
    _ = sendMediaRemote(cmd: kMRPause)
    sendHardwareKey(key: NX_KEYTYPE_PLAY)
    print("Action pause executed")

case "next":
    _ = sendMediaRemote(cmd: kMRNextTrack)
    sendHardwareKey(key: NX_KEYTYPE_NEXT)
    print("Action next executed")

case "prev", "previous":
    _ = sendMediaRemote(cmd: kMRPreviousTrack)
    sendHardwareKey(key: NX_KEYTYPE_PREVIOUS)
    print("Action prev executed")

case "seek_forward", "forward", "forward_15":
    sendHardwareKey(key: 19)
    let s = """
    try
        tell application "Spotify" to if player state is playing then set player position to (player position + 15)
    end try
    try
        tell application "Music" to if player state is playing then set player position to (player position + 15)
    end try
    """
    var err: NSDictionary?
    NSAppleScript(source: s)?.executeAndReturnError(&err)
    print("Action seek_forward executed")

case "seek_backward", "rewind", "rewind_15":
    sendHardwareKey(key: 20)
    let s = """
    try
        tell application "Spotify" to if player state is playing then set player position to (player position - 15)
    end try
    try
        tell application "Music" to if player state is playing then set player position to (player position - 15)
    end try
    """
    var err: NSDictionary?
    NSAppleScript(source: s)?.executeAndReturnError(&err)
    print("Action seek_backward executed")

case "volume_up", "volup":
    sendHardwareKey(key: NX_KEYTYPE_SOUND_UP)
    print("Action volume_up executed")

case "volume_down", "voldown":
    sendHardwareKey(key: NX_KEYTYPE_SOUND_DOWN)
    print("Action volume_down executed")

case "mute":
    sendHardwareKey(key: NX_KEYTYPE_MUTE)
    print("Action mute executed")

default:
    _ = sendMediaRemote(cmd: kMRTogglePlayPause)
    sendHardwareKey(key: NX_KEYTYPE_PLAY)
    print("Action default executed")
}
