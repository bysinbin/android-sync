import SwiftUI
import WatchKit

struct SettingsView: View {
    @EnvironmentObject var syncService: WatchSyncService
    @StateObject private var discovery = NetworkDiscovery()
    
    @State private var showingPinInput: Bool = false
    @State private var inputPin: String = ""
    @State private var pairingAlertMessage: String = ""
    @State private var showingPairingAlert: Bool = false
    @State private var isPairingLoading: Bool = false
    
    var effectivePin: String {
        if !syncService.pairingPin.isEmpty {
            return syncService.pairingPin
        } else if !discovery.activePairingPin.isEmpty {
            return discovery.activePairingPin
        }
        return "170260"
    }
    
    var isPhonePrimary: Bool {
        return syncService.serverPort == 42426 || syncService.selectedSource == .phone
    }
    
    var body: some View {
        ScrollView {
            VStack(spacing: 8) {
                // Header & Connection Pill
                HStack {
                    Text("Ana Cihaz: Telefon 📱")
                        .font(.system(size: 13, weight: .bold))
                    Spacer()
                    Circle()
                        .fill(syncService.isConnected ? Color.green : Color.orange)
                        .frame(width: 8, height: 8)
                }
                .padding(.horizontal, 4)
                
                // 1. 📱 ANA CİHAZ: ANDROID TELEFON (DOĞRUDAN EŞLEŞME)
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Image(systemName: "iphone.radiowaves.left.and.right")
                            .foregroundColor(.green)
                            .font(.system(size: 12))
                        Text("Android Telefon (Ana Cihaz)")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundColor(.primary)
                        Spacer()
                        Text(syncService.isPaired ? "Eşleşti 🔒" : "Eşleşme Bekleniyor ⚠️")
                            .font(.system(size: 8, weight: .bold))
                            .foregroundColor(syncService.isPaired ? .green : .orange)
                    }
                    
                    let phones = discovery.discoveredPhones
                    let phoneName = !phones.isEmpty ? phones[0].name : (syncService.deviceInfo.deviceName.isEmpty ? "2409BRN2CA" : syncService.deviceInfo.deviceName)
                    let phoneBatt = !phones.isEmpty ? (phones[0].batteryLevel ?? 52) : (syncService.deviceInfo.batteryLevel > 0 ? syncService.deviceInfo.batteryLevel : 52)
                    let phoneHost = !phones.isEmpty ? phones[0].host : "192.168.50.118"
                    
                    Button(action: {
                        syncService.connectDirectToPhone()
                    }) {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("\(phoneName)")
                                    .font(.system(size: 11, weight: .bold))
                                    .foregroundColor(.white)
                                Text("Doğrudan Telefon (Wi-Fi/Port: 42424)")
                                    .font(.system(size: 8))
                                    .foregroundColor(.white.opacity(0.8))
                                Text("IP: \(phoneHost)")
                                    .font(.system(size: 8, design: .monospaced))
                                    .foregroundColor(.white.opacity(0.6))
                            }
                            Spacer()
                            VStack(alignment: .trailing, spacing: 2) {
                                Text("%\(phoneBatt) 🔋")
                                    .font(.system(size: 10, weight: .bold))
                                    .foregroundColor(.green)
                                Text(isPhonePrimary ? "ANA CİHAZ ⚡" : "Bağlan")
                                    .font(.system(size: 8, weight: .bold))
                                    .foregroundColor(isPhonePrimary ? .yellow : .cyan)
                            }
                        }
                        .padding(7)
                        .background(Color.green.opacity(0.25))
                        .cornerRadius(8)
                        .overlay(
                            RoundedRectangle(cornerRadius: 8)
                                .stroke(Color.green.opacity(0.5), lineWidth: 1)
                        )
                    }
                    .buttonStyle(.plain)
                }
                .padding(7)
                .background(Color.white.opacity(0.06))
                .cornerRadius(10)
                
                // 2. 🔒 GÜVENLİ EŞLEŞTİRME (PIN KODU) KARTI
                VStack(alignment: .leading, spacing: 5) {
                    HStack {
                        Image(systemName: "lock.shield.fill")
                            .foregroundColor(syncService.isPaired ? .green : .orange)
                        Text("Telefon Eşleştirme (PIN)")
                            .font(.system(size: 11, weight: .bold))
                        Spacer()
                    }
                    
                    // 6 Haneli PIN Kutusu
                    VStack(spacing: 1) {
                        Text("Telefonda Görünen Doğrulama PIN:")
                            .font(.system(size: 8))
                            .foregroundColor(.secondary)
                        
                        Text(formatPin(effectivePin))
                            .font(.system(size: 17, weight: .black, design: .monospaced))
                            .foregroundColor(.cyan)
                            .kerning(3)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 3)
                            .background(Color.black.opacity(0.4))
                            .cornerRadius(6)
                            .overlay(
                                RoundedRectangle(cornerRadius: 6)
                                    .stroke(Color.cyan.opacity(0.4), lineWidth: 1)
                            )
                    }
                    
                    // PIN ile Eşleştir / Onayla Butonu
                    Button(action: {
                        isPairingLoading = true
                        let pinToConfirm = inputPin.isEmpty ? effectivePin : inputPin
                        syncService.confirmPairing(pin: pinToConfirm) { success, msg in
                            isPairingLoading = false
                            pairingAlertMessage = msg
                            showingPairingAlert = true
                        }
                    }) {
                        HStack {
                            if isPairingLoading {
                                ProgressView()
                                    .scaleEffect(0.6)
                            } else {
                                Image(systemName: syncService.isPaired ? "checkmark.seal.fill" : "key.fill")
                                    .font(.system(size: 10))
                                Text(syncService.isPaired ? "Telefonla Eşleşti 🔒" : "Telefonla Eşleştir")
                                    .font(.system(size: 10, weight: .bold))
                            }
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 5)
                        .background(syncService.isPaired ? Color.green.opacity(0.8) : Color.blue)
                        .cornerRadius(7)
                    }
                    .buttonStyle(.plain)
                    
                    // Alt butonlar: PIN Gir & Yeni PIN
                    HStack(spacing: 4) {
                        Button(action: {
                            inputPin = (effectivePin != "------" ? effectivePin : "")
                            showingPinInput = true
                        }) {
                            HStack {
                                Image(systemName: "keyboard")
                                    .font(.system(size: 8))
                                Text("PIN Gir")
                                    .font(.system(size: 9))
                            }
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 4)
                            .background(Color.white.opacity(0.1))
                            .cornerRadius(6)
                        }
                        .buttonStyle(.plain)
                        
                        Button(action: {
                            syncService.resetPairing { success, newPin in
                                if success {
                                    pairingAlertMessage = "Yeni PIN: \(newPin)"
                                    showingPairingAlert = true
                                }
                            }
                        }) {
                            HStack {
                                Image(systemName: "arrow.triangle.2.circlepath")
                                    .font(.system(size: 8))
                                Text("Yeni PIN")
                                    .font(.system(size: 9))
                            }
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 4)
                            .background(Color.white.opacity(0.1))
                            .cornerRadius(6)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(7)
                .background(Color.cyan.opacity(0.1))
                .cornerRadius(10)
                .overlay(
                    RoundedRectangle(cornerRadius: 10)
                        .stroke(Color.cyan.opacity(0.25), lineWidth: 1)
                )
                
                // 3. 💻 İKİNCİL / YEDEK (Mac Bilgisayar)
                VStack(alignment: .leading, spacing: 3) {
                    HStack {
                        Image(systemName: "laptopcomputer")
                            .foregroundColor(.blue)
                            .font(.system(size: 10))
                        Text("Mac Bilgisayar (İkincil)")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundColor(.secondary)
                        Spacer()
                    }
                    
                    HStack {
                        Text("192.168.50.96:42424")
                            .font(.system(size: 8, design: .monospaced))
                            .foregroundColor(.secondary)
                        Spacer()
                        Button("Mac Bağlan") {
                            WKInterfaceDevice.current().play(.click)
                            syncService.serverHost = "127.0.0.1"
                            syncService.serverPort = 42424
                            syncService.selectedSource = .mac
                            syncService.connect()
                        }
                        .font(.system(size: 8))
                        .buttonStyle(.plain)
                        .foregroundColor(.blue)
                    }
                }
                .padding(5)
                .background(Color.white.opacity(0.04))
                .cornerRadius(7)
            }
            .padding(.horizontal, 4)
        }
        .onAppear {
            syncService.connectDirectToPhone()
            discovery.startDiscovery()
            syncService.fetchStatus()
        }
        .alert(isPresented: $showingPairingAlert) {
            Alert(
                title: Text("Eşleştirme"),
                message: Text(pairingAlertMessage),
                dismissButton: .default(Text("Tamam"))
            )
        }
        .sheet(isPresented: $showingPinInput) {
            PinInputView(inputPin: $inputPin) { enteredPin in
                showingPinInput = false
                isPairingLoading = true
                syncService.confirmPairing(pin: enteredPin) { success, msg in
                    isPairingLoading = false
                    pairingAlertMessage = msg
                    showingPairingAlert = true
                }
            }
        }
    }
    
    private func formatPin(_ pin: String) -> String {
        let clean = pin.replacingOccurrences(of: " ", with: "")
        if clean.count == 6 {
            let p1 = clean.prefix(3)
            let p2 = clean.suffix(3)
            return "\(p1) \(p2)"
        }
        return pin
    }
}

// MARK: - PinInputView (WatchOS Sheet for PIN)
struct PinInputView: View {
    @Binding var inputPin: String
    var onConfirm: (String) -> Void
    @Environment(\.presentationMode) var presentationMode
    
    var body: some View {
        VStack(spacing: 8) {
            Text("PIN Girin")
                .font(.system(size: 12, weight: .bold))
            
            TextField("6 Haneli PIN", text: $inputPin)
                .font(.system(size: 14, weight: .bold, design: .monospaced))
                .multilineTextAlignment(.center)
            
            Button("Eşleştirmeyi Onayla") {
                WKInterfaceDevice.current().play(.click)
                onConfirm(inputPin)
            }
            .background(Color.green)
            .cornerRadius(8)
            
            Button("İptal") {
                presentationMode.wrappedValue.dismiss()
            }
            .foregroundColor(.secondary)
        }
        .padding()
    }
}
