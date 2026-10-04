import SwiftUI
import WatchKit

struct MediaView: View {
    @EnvironmentObject var syncService: WatchSyncService
    @State private var crownVolume: Double = 50.0
    @State private var lastCrownTime: Date = Date()
    @State private var isShowingVolumeIndicator: Bool = false
    @State private var hideVolumeTimer: Timer?
    
    var media: MediaInfo {
        syncService.activeMedia
    }
    
    var body: some View {
        ScrollView {
            VStack(spacing: 8) {
                // MARK: - Dual Device Simultaneous Live Status Bar
                HStack(spacing: 8) {
                    HStack(spacing: 3) {
                        Image(systemName: "laptopcomputer")
                            .font(.system(size: 9))
                            .foregroundColor(syncService.isConnected ? .green : .red)
                        Text("Mac")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundColor(syncService.isConnected ? .primary : .secondary)
                    }
                    
                    Text("•")
                        .font(.system(size: 8))
                        .foregroundColor(.secondary)
                    
                    HStack(spacing: 3) {
                        Image(systemName: "iphone")
                            .font(.system(size: 9))
                            .foregroundColor(syncService.isPhoneConnected ? .green : .orange)
                        Text(syncService.isPhoneConnected ? "Tel %\(syncService.deviceInfo.batteryLevel)" : "Tel Bekleniyor")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundColor(syncService.isPhoneConnected ? .primary : .secondary)
                        if syncService.deviceInfo.isCharging {
                            Image(systemName: "bolt.fill")
                                .font(.system(size: 8))
                                .foregroundColor(.yellow)
                        }
                    }
                }
                .padding(.vertical, 3)
                .padding(.horizontal, 8)
                .background(Color.white.opacity(0.08))
                .cornerRadius(10)
                
                // MARK: - Source Selector (Mac vs Telefon - Çift Yönlü Eşzamanlı Kontrol)
                HStack(spacing: 6) {
                    ForEach(MediaSource.allCases) { source in
                        let isThisPlaying = (source == .mac ? syncService.macMedia.isPlaying : syncService.phoneMedia.isPlaying)
                        Button(action: {
                            WKInterfaceDevice.current().play(.click)
                            syncService.selectedSource = source
                        }) {
                            HStack(spacing: 4) {
                                Image(systemName: source.iconName)
                                    .font(.system(size: 11, weight: .semibold))
                                Text(source.title)
                                    .font(.system(size: 11, weight: .bold))
                                
                                if isThisPlaying {
                                    Circle()
                                        .fill(Color.green)
                                        .frame(width: 5, height: 5)
                                }
                            }
                            .padding(.vertical, 4)
                            .padding(.horizontal, 8)
                            .frame(maxWidth: .infinity)
                            .background(
                                syncService.selectedSource == source
                                ? Color.blue.opacity(0.85)
                                : Color.white.opacity(0.12)
                            )
                            .cornerRadius(12)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.top, 1)
                
                // MARK: - Track Info
                VStack(spacing: 2) {
                    Text(media.title.isEmpty ? "Medyada Bir Şey Çalmıyor" : media.title)
                        .font(.system(size: 14, weight: .bold))
                        .foregroundColor(.primary)
                        .lineLimit(1)
                        .multilineTextAlignment(.center)
                    
                    Text(media.artist.isEmpty ? "Beklemede..." : media.artist)
                        .font(.system(size: 11, weight: .medium))
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                }
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 4)
                
                // MARK: - Progress & Timeline
                VStack(spacing: 3) {
                    GeometryReader { geo in
                        ZStack(alignment: .leading) {
                            Capsule()
                                .fill(Color.white.opacity(0.2))
                                .frame(height: 5)
                            
                            Capsule()
                                .fill(LinearGradient(colors: [.cyan, .blue], startPoint: .leading, endPoint: .trailing))
                                .frame(width: max(0, min(geo.size.width, geo.size.width * CGFloat(media.percent))), height: 5)
                        }
                        .gesture(
                            DragGesture(minimumDistance: 0)
                                .onEnded { value in
                                    let pct = Double(value.location.x / geo.size.width)
                                    syncService.seek(percent: pct)
                                }
                        )
                    }
                    .frame(height: 5)
                    
                    HStack {
                        Text(media.formattedPosition)
                            .font(.system(size: 9, weight: .medium, design: .monospaced))
                            .foregroundColor(.secondary)
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
                            .font(.system(size: 16))
                            .frame(width: 38, height: 38)
                            .background(Color.white.opacity(0.12))
                            .clipShape(Circle())
                    }
                    .buttonStyle(.plain)
                    
                    // Play / Pause
                    Button(action: {
                        syncService.playPause()
                    }) {
                        Image(systemName: media.isPlaying ? "pause.fill" : "play.fill")
                            .font(.system(size: 20, weight: .bold))
                            .foregroundColor(.black)
                            .frame(width: 50, height: 50)
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
                            .font(.system(size: 16))
                            .frame(width: 38, height: 38)
                            .background(Color.white.opacity(0.12))
                            .clipShape(Circle())
                    }
                    .buttonStyle(.plain)
                }
                .padding(.vertical, 4)
                
                // MARK: - Digital Crown Volume Indicator
                HStack(spacing: 6) {
                    Image(systemName: isShowingVolumeIndicator ? "speaker.wave.3.fill" : "digitalcrown.horizontal.press.fill")
                        .font(.system(size: 10))
                        .foregroundColor(.secondary)
                    
                    Text(isShowingVolumeIndicator ? "Ses: %\(Int(crownVolume))" : "Ses için Digital Crown çevirin")
                        .font(.system(size: 10, weight: .medium))
                        .foregroundColor(isShowingVolumeIndicator ? .cyan : .secondary)
                }
                .padding(.vertical, 2)
            }
            .padding(.horizontal, 6)
        }
        .focusable(true)
        .digitalCrownRotation(
            $crownVolume,
            from: 0.0,
            through: 100.0,
            by: 2.0,
            sensitivity: .medium,
            isContinuous: false,
            isHapticFeedbackEnabled: true
        )
        .onChange(of: crownVolume) { newValue in
            handleCrownRotation(newValue)
        }
        .onAppear {
            crownVolume = syncService.volumeLevel
        }
    }
    
    private func handleCrownRotation(_ newVol: Double) {
        let now = Date()
        guard now.timeIntervalSince(lastCrownTime) > 0.15 else { return }
        lastCrownTime = now
        
        let delta = newVol - syncService.volumeLevel
        if abs(delta) >= 2.0 {
            syncService.adjustVolume(by: delta)
            showVolumeOverlay()
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
}
