import SwiftUI
import WatchKit

enum CrownMode: String, CaseIterable, Identifiable {
    case volume
    case seek
    
    var id: String { rawValue }
    
    var title: String {
        switch self {
        case .volume: return "Ses"
        case .seek: return "Süre"
        }
    }
    
    var iconName: String {
        switch self {
        case .volume: return "speaker.wave.2.fill"
        case .seek: return "clock.arrow.circlepath"
        }
    }
}

struct MediaView: View {
    @EnvironmentObject var syncService: WatchSyncService
    
    // Crown Control Mode: Ses (Volume) vs Süre (Seek)
    @State private var crownMode: CrownMode = .volume
    @State private var crownValue: Double = 50.0
    @State private var lastCrownValue: Double = 50.0
    @State private var lastCrownTime: Date = Date()
    
    // Volume overlay state
    @State private var isShowingVolumeIndicator: Bool = false
    @State private var hideVolumeTimer: Timer?
    
    // Seek overlay & scrubbing state
    @State private var isUserScrubbing: Bool = false
    @State private var scrubPercent: Double = 0.0
    @State private var seekDebounceTimer: Timer?
    @State private var isShowingSeekIndicator: Bool = false
    @State private var hideSeekTimer: Timer?
    
    var media: MediaInfo {
        syncService.activeMedia
    }
    
    var isPhoneActive: Bool {
        syncService.isPhoneConnected || syncService.isConnected
    }
    
    // Active percentage to display on timeline (0.0 to 1.0)
    var currentDisplayPercent: Double {
        if crownMode == .seek && isUserScrubbing {
            return max(0.0, min(1.0, scrubPercent / 100.0))
        }
        return max(0.0, min(1.0, media.percent))
    }
    
    // Current display position string (e.g. "1:38")
    var currentDisplayPosition: String {
        if crownMode == .seek && (isUserScrubbing || isShowingSeekIndicator) {
            guard media.durationMs > 0 else {
                return "%\(Int(scrubPercent))"
            }
            let scrubMs = Int64(Double(media.durationMs) * (scrubPercent / 100.0))
            let totalSec = max(0, scrubMs / 1000)
            let m = totalSec / 60
            let s = totalSec % 60
            return String(format: "%d:%02d", m, s)
        }
        return media.formattedPosition
    }
    
    // Scrub offset delta relative to original track position (e.g. "+14 sn")
    var scrubDeltaText: String? {
        guard crownMode == .seek, (isUserScrubbing || isShowingSeekIndicator), media.durationMs > 0 else { return nil }
        let currentSec = Int64(Double(media.durationMs) * (scrubPercent / 100.0) / 1000)
        let origSec = max(0, media.positionMs / 1000)
        let diff = currentSec - origSec
        if diff > 0 {
            return "+\(diff) sn"
        } else if diff < 0 {
            return "\(diff) sn"
        }
        return nil
    }
    
    var body: some View {
        ScrollView {
            VStack(spacing: 5) {
                // MARK: - Dual Device Simultaneous Live Status Bar
                HStack(spacing: 6) {
                    // 1. Ana Cihaz: Telefon
                    HStack(spacing: 3) {
                        Image(systemName: "iphone")
                            .font(.system(size: 9))
                            .foregroundColor(isPhoneActive ? .green : .orange)
                        Text(isPhoneActive ? "Tel %\(syncService.deviceInfo.batteryLevel)" : "Tel Bekleniyor")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundColor(isPhoneActive ? .primary : .secondary)
                        if syncService.deviceInfo.isCharging {
                            Image(systemName: "bolt.fill")
                                .font(.system(size: 8))
                                .foregroundColor(.yellow)
                        }
                    }
                    
                    Text("•")
                        .font(.system(size: 8))
                        .foregroundColor(.secondary)
                    
                    // 2. İkincil Cihaz: Mac
                    HStack(spacing: 3) {
                        Image(systemName: "laptopcomputer")
                            .font(.system(size: 9))
                            .foregroundColor(.cyan)
                        Text("Mac")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundColor(.cyan)
                    }
                    
                    if !syncService.notifications.isEmpty {
                        Text("•")
                            .font(.system(size: 8))
                            .foregroundColor(.secondary)
                        
                        Button(action: {
                            WKInterfaceDevice.current().play(.click)
                            withAnimation {
                                syncService.currentTab = 1
                            }
                        }) {
                            HStack(spacing: 2) {
                                Image(systemName: "bell.fill")
                                    .font(.system(size: 8))
                                    .foregroundColor(.orange)
                                Text("\(syncService.notifications.count)")
                                    .font(.system(size: 9, weight: .bold))
                                    .foregroundColor(.orange)
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.vertical, 2)
                .padding(.horizontal, 7)
                .background(Color.white.opacity(0.08))
                .cornerRadius(10)
                
                // MARK: - Source Selector (Telefon vs Mac)
                HStack(spacing: 5) {
                    ForEach(MediaSource.allCases) { source in
                        let isThisPlaying = (source == .mac ? syncService.macMedia.isPlaying : syncService.phoneMedia.isPlaying)
                        
                        Button(action: {
                            WKInterfaceDevice.current().play(.click)
                            syncService.selectedSource = source
                        }) {
                            HStack(spacing: 4) {
                                Image(systemName: source.iconName)
                                    .font(.system(size: 10, weight: .semibold))
                                Text(source.title)
                                    .font(.system(size: 10, weight: .bold))
                                
                                if isThisPlaying {
                                    Circle()
                                        .fill(Color.green)
                                        .frame(width: 5, height: 5)
                                }
                            }
                            .padding(.vertical, 3)
                            .padding(.horizontal, 6)
                            .frame(maxWidth: .infinity)
                            .background(
                                syncService.selectedSource == source
                                ? (source == .phone ? Color.green.opacity(0.85) : Color.blue.opacity(0.85))
                                : Color.white.opacity(0.12)
                            )
                            .cornerRadius(10)
                        }
                        .buttonStyle(.plain)
                    }
                }
                
                // MARK: - Crown Mode Selector (Ses vs Süre)
                HStack(spacing: 5) {
                    // Ses (Volume) Mode Button
                    Button(action: {
                        setCrownMode(.volume)
                    }) {
                        HStack(spacing: 4) {
                            Image(systemName: "speaker.wave.2.fill")
                                .font(.system(size: 10, weight: .bold))
                            Text("Ses")
                                .font(.system(size: 10, weight: .bold))
                            if crownMode == .volume {
                                Circle()
                                    .fill(Color.black)
                                    .frame(width: 4, height: 4)
                            }
                        }
                        .padding(.vertical, 3)
                        .padding(.horizontal, 6)
                        .frame(maxWidth: .infinity)
                        .background(
                            crownMode == .volume
                            ? Color.cyan
                            : Color.white.opacity(0.12)
                        )
                        .foregroundColor(crownMode == .volume ? .black : .primary)
                        .cornerRadius(10)
                    }
                    .buttonStyle(.plain)
                    
                    // Süre (Seek) Mode Button
                    Button(action: {
                        setCrownMode(.seek)
                    }) {
                        HStack(spacing: 4) {
                            Image(systemName: "clock.arrow.circlepath")
                                .font(.system(size: 10, weight: .bold))
                            Text("Süre")
                                .font(.system(size: 10, weight: .bold))
                            if crownMode == .seek {
                                Circle()
                                    .fill(Color.black)
                                    .frame(width: 4, height: 4)
                            }
                        }
                        .padding(.vertical, 3)
                        .padding(.horizontal, 6)
                        .frame(maxWidth: .infinity)
                        .background(
                            crownMode == .seek
                            ? Color.orange
                            : Color.white.opacity(0.12)
                        )
                        .foregroundColor(crownMode == .seek ? .black : .primary)
                        .cornerRadius(10)
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 1)
                
                // MARK: - Track Info
                VStack(spacing: 1) {
                    Text(media.title.isEmpty ? (syncService.selectedSource == .phone ? "Telefon Medyası Hazır" : "Mac Medyası Hazır") : media.title)
                        .font(.system(size: 12, weight: .bold))
                        .foregroundColor(.primary)
                        .lineLimit(1)
                        .multilineTextAlignment(.center)
                    
                    Text(media.artist.isEmpty ? (isPhoneActive ? "Çalmak için dokunun" : "Beklemede...") : media.artist)
                        .font(.system(size: 10, weight: .medium))
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                }
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 4)
                
                // MARK: - Progress & Timeline (Interactive & Scrubber)
                VStack(spacing: 2) {
                    GeometryReader { geo in
                        ZStack(alignment: .leading) {
                            // Track background
                            Capsule()
                                .fill(Color.white.opacity(0.2))
                                .frame(height: crownMode == .seek ? 7 : 5)
                            
                            // Filled progress bar
                            Capsule()
                                .fill(
                                    crownMode == .seek
                                    ? LinearGradient(colors: [.orange, .yellow], startPoint: .leading, endPoint: .trailing)
                                    : LinearGradient(colors: [.cyan, .green], startPoint: .leading, endPoint: .trailing)
                                )
                                .frame(
                                    width: max(0, min(geo.size.width, geo.size.width * CGFloat(currentDisplayPercent))),
                                    height: crownMode == .seek ? 7 : 5
                                )
                            
                            // Scrubbing Thumb Knob when in Seek Mode
                            if crownMode == .seek {
                                Circle()
                                    .fill(Color.white)
                                    .frame(width: 12, height: 12)
                                    .shadow(color: .orange.opacity(0.8), radius: 3)
                                    .offset(x: max(0, min(geo.size.width - 12, geo.size.width * CGFloat(currentDisplayPercent) - 6)))
                            }
                        }
                        .gesture(
                            DragGesture(minimumDistance: 0)
                                .onEnded { value in
                                    let pct = max(0.0, min(1.0, Double(value.location.x / geo.size.width)))
                                    setCrownMode(.seek)
                                    crownValue = pct * 100.0
                                    scrubPercent = crownValue
                                    syncService.seek(percent: pct)
                                }
                        )
                    }
                    .frame(height: crownMode == .seek ? 12 : 5)
                    .onTapGesture {
                        setCrownMode(.seek)
                    }
                    
                    // Timestamps and Scrub Offset
                    HStack {
                        Text(currentDisplayPosition)
                            .font(.system(size: 9, weight: .bold, design: .monospaced))
                            .foregroundColor(crownMode == .seek ? .orange : .secondary)
                        
                        if let delta = scrubDeltaText {
                            Spacer()
                            Text(delta)
                                .font(.system(size: 8, weight: .bold, design: .monospaced))
                                .foregroundColor(.yellow)
                                .padding(.horizontal, 4)
                                .padding(.vertical, 1)
                                .background(Color.black.opacity(0.5))
                                .cornerRadius(4)
                        }
                        
                        Spacer()
                        
                        Text(media.formattedDuration)
                            .font(.system(size: 9, weight: .medium, design: .monospaced))
                            .foregroundColor(.secondary)
                    }
                }
                .padding(.horizontal, 4)
                
                // MARK: - Playback Controls
                HStack(spacing: 12) {
                    // Previous
                    Button(action: {
                        syncService.previousTrack()
                    }) {
                        Image(systemName: "backward.fill")
                            .font(.system(size: 15))
                            .frame(width: 36, height: 36)
                            .background(Color.white.opacity(0.12))
                            .clipShape(Circle())
                    }
                    .buttonStyle(.plain)
                    
                    // Play / Pause
                    Button(action: {
                        syncService.playPause()
                    }) {
                        Image(systemName: media.isPlaying ? "pause.fill" : "play.fill")
                            .font(.system(size: 19, weight: .bold))
                            .foregroundColor(.black)
                            .frame(width: 44, height: 44)
                            .background(
                                LinearGradient(
                                    colors: media.isPlaying ? [Color.green, Color.mint] : [Color.cyan, Color.blue],
                                    startPoint: .topLeading,
                                    endPoint: .bottomTrailing
                                )
                            )
                            .clipShape(Circle())
                            .shadow(color: (media.isPlaying ? Color.green : Color.blue).opacity(0.4), radius: 6, x: 0, y: 2)
                    }
                    .buttonStyle(.plain)
                    
                    // Next
                    Button(action: {
                        syncService.nextTrack()
                    }) {
                        Image(systemName: "forward.fill")
                            .font(.system(size: 15))
                            .frame(width: 36, height: 36)
                            .background(Color.white.opacity(0.12))
                            .clipShape(Circle())
                    }
                    .buttonStyle(.plain)
                }
                .padding(.vertical, 1)
                
                // MARK: - Digital Crown Interactive Mode Pill
                Button(action: {
                    setCrownMode(crownMode == .volume ? .seek : .volume)
                }) {
                    HStack(spacing: 5) {
                        Image(systemName: crownMode == .volume ? (isShowingVolumeIndicator ? "speaker.wave.3.fill" : "speaker.wave.2.fill") : "clock.arrow.circlepath")
                            .font(.system(size: 9))
                            .foregroundColor(crownMode == .volume ? .cyan : .orange)
                        
                        if crownMode == .volume {
                            Text(isShowingVolumeIndicator ? "Ses: %\(Int(crownValue))" : "Crown: Ses Seviyesi")
                                .font(.system(size: 9, weight: .medium))
                                .foregroundColor(isShowingVolumeIndicator ? .cyan : .secondary)
                        } else {
                            Text(isShowingSeekIndicator ? "Süre: \(currentDisplayPosition)" : "Crown: Şarkıyı Sar")
                                .font(.system(size: 9, weight: .medium))
                                .foregroundColor(isShowingSeekIndicator ? .orange : .secondary)
                        }
                        
                        Spacer()
                        
                        Image(systemName: "digitalcrown.horizontal.press.fill")
                            .font(.system(size: 9))
                            .foregroundColor((crownMode == .volume ? Color.cyan : Color.orange).opacity(0.85))
                    }
                    .padding(.vertical, 3)
                    .padding(.horizontal, 7)
                    .background((crownMode == .volume ? Color.cyan : Color.orange).opacity(0.12))
                    .cornerRadius(7)
                }
                .buttonStyle(.plain)
                .padding(.horizontal, 2)
            }
            .padding(.horizontal, 4)
        }
        .focusable(true)
        .digitalCrownRotation(
            $crownValue,
            from: 0.0,
            through: 100.0,
            by: 1.0,
            sensitivity: .medium,
            isContinuous: false,
            isHapticFeedbackEnabled: true
        )
        .onChange(of: crownValue) { newValue in
            handleCrownRotation(newValue)
        }
        .onChange(of: syncService.activeMedia.percent) { newPercent in
            if crownMode == .seek && !isUserScrubbing {
                let p = (newPercent > 1.0 ? newPercent / 100.0 : newPercent) * 100.0
                crownValue = max(0.0, min(100.0, p))
                scrubPercent = crownValue
                lastCrownValue = crownValue
            }
        }
        .onChange(of: syncService.volumeLevel) { newVol in
            if crownMode == .volume {
                crownValue = newVol
                lastCrownValue = newVol
            }
        }
        .onAppear {
            crownValue = syncService.volumeLevel
            lastCrownValue = crownValue
            syncService.fetchStatus()
        }
    }
    
    // MARK: - Crown Mode Switcher
    private func setCrownMode(_ mode: CrownMode) {
        WKInterfaceDevice.current().play(.click)
        withAnimation(.easeInOut(duration: 0.2)) {
            crownMode = mode
        }
        if mode == .volume {
            crownValue = syncService.volumeLevel
            lastCrownValue = crownValue
            showVolumeOverlay()
        } else {
            let p = (media.percent > 1.0 ? media.percent / 100.0 : media.percent) * 100.0
            crownValue = max(0.0, min(100.0, p))
            scrubPercent = crownValue
            lastCrownValue = crownValue
            showSeekOverlay()
        }
    }
    
    // MARK: - Crown Rotation Handling
    private func handleCrownRotation(_ newValue: Double) {
        if crownMode == .volume {
            let delta = newValue - lastCrownValue
            if abs(delta) >= 2.0 {
                lastCrownValue = newValue
                syncService.adjustVolume(by: delta)
                WKInterfaceDevice.current().play(.click)
                showVolumeOverlay()
            }
        } else {
            let delta = newValue - lastCrownValue
            if abs(delta) >= 0.5 {
                lastCrownValue = newValue
                isUserScrubbing = true
                scrubPercent = max(0.0, min(100.0, newValue))
                WKInterfaceDevice.current().play(.click)
                showSeekOverlay()
                
                // Debounce seek network request
                seekDebounceTimer?.invalidate()
                seekDebounceTimer = Timer.scheduledTimer(withTimeInterval: 0.25, repeats: false) { _ in
                    let targetPct = self.scrubPercent / 100.0
                    self.syncService.seek(percent: targetPct)
                    
                    // Allow background updates to resume after 1.2s
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
                        self.isUserScrubbing = false
                    }
                }
            }
        }
    }
    
    private func showVolumeOverlay() {
        isShowingVolumeIndicator = true
        hideVolumeTimer?.invalidate()
        hideVolumeTimer = Timer.scheduledTimer(withTimeInterval: 1.5, repeats: false) { _ in
            withAnimation {
                self.isShowingVolumeIndicator = false
            }
        }
    }
    
    private func showSeekOverlay() {
        isShowingSeekIndicator = true
        hideSeekTimer?.invalidate()
        hideSeekTimer = Timer.scheduledTimer(withTimeInterval: 1.8, repeats: false) { _ in
            withAnimation {
                self.isShowingSeekIndicator = false
            }
        }
    }
}
