import SwiftUI
import WatchKit

struct IncomingCallSheet: View {
    @EnvironmentObject var syncService: WatchSyncService
    
    var callerTitle: String {
        if !syncService.callState.callerName.isEmpty {
            return syncService.callState.callerName
        }
        if !syncService.callState.phoneNumber.isEmpty {
            return syncService.callState.phoneNumber
        }
        return "Bilinmeyen Arayan"
    }
    
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: "phone.down.circle.fill")
                .foregroundColor(.green)
                .font(.system(size: 32))
                .padding(.top, 4)
            
            Text("Gelen Arama")
                .font(.system(size: 11, weight: .semibold))
                .foregroundColor(.secondary)
            
            Text(callerTitle)
                .font(.system(size: 14, weight: .bold))
                .lineLimit(1)
                .multilineTextAlignment(.center)
            
            if !syncService.callState.callerName.isEmpty && !syncService.callState.phoneNumber.isEmpty {
                Text(syncService.callState.phoneNumber)
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.secondary)
            }
            
            Spacer()
            
            HStack(spacing: 16) {
                // Reject Button
                Button(action: {
                    syncService.rejectCall()
                }) {
                    Image(systemName: "phone.down.fill")
                        .font(.system(size: 18))
                        .foregroundColor(.white)
                        .frame(width: 48, height: 48)
                        .background(Color.red)
                        .clipShape(Circle())
                }
                .buttonStyle(.plain)
                
                // Answer Button
                Button(action: {
                    syncService.answerCall()
                }) {
                    Image(systemName: "phone.fill")
                        .font(.system(size: 18))
                        .foregroundColor(.white)
                        .frame(width: 48, height: 48)
                        .background(Color.green)
                        .clipShape(Circle())
                }
                .buttonStyle(.plain)
            }
            .padding(.bottom, 6)
        }
        .padding(.horizontal, 6)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color.black.edgesIgnoringSafeArea(.all))
        .onAppear {
            startRingHaptics()
        }
        .onDisappear {
            stopRingHaptics()
        }
    }
    
    @State private var ringTimer: Timer?
    
    private func startRingHaptics() {
        ringTimer?.invalidate()
        WKInterfaceDevice.current().play(.notification)
        ringTimer = Timer.scheduledTimer(withTimeInterval: 1.2, repeats: true) { _ in
            WKInterfaceDevice.current().play(.notification)
        }
    }
    
    private func stopRingHaptics() {
        ringTimer?.invalidate()
        ringTimer = nil
    }
}
