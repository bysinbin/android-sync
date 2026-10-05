import Foundation
import CoreBluetooth
import WatchKit

class WatchBluetoothService: NSObject, ObservableObject, CBCentralManagerDelegate, CBPeripheralDelegate {
    static let shared = WatchBluetoothService()
    
    // MacSync Custom BLE GATT Service & Characteristics
    static let serviceUUID   = CBUUID(string: "9A48C210-1B2C-4C92-A62E-8463B81A0001")
    static let rxCharUUID    = CBUUID(string: "9A48C210-1B2C-4C92-A62E-8463B81A0002") // Watch -> Phone (Write)
    static let txCharUUID    = CBUUID(string: "9A48C210-1B2C-4C92-A62E-8463B81A0003") // Phone -> Watch (Notify)
    
    @Published var isBluetoothPoweredOn: Bool = false
    @Published var isConnected: Bool = false
    @Published var connectedDeviceName: String? = nil
    
    private var centralManager: CBCentralManager!
    private var targetPeripheral: CBPeripheral?
    private var rxCharacteristic: CBCharacteristic?
    private var txCharacteristic: CBCharacteristic?
    private var scanRetryTimer: Timer?
    
    override init() {
        super.init()
        self.centralManager = CBCentralManager(delegate: self, queue: .main)
    }
    
    func startScan() {
        guard centralManager.state == .poweredOn else { return }
        print("🔵 [WatchBLE] Tarama başlatılıyor...")
        centralManager.scanForPeripherals(withServices: [Self.serviceUUID], options: [CBCentralManagerScanOptionAllowDuplicatesKey: false])
    }
    
    func stopScan() {
        centralManager.stopScan()
        scanRetryTimer?.invalidate()
        scanRetryTimer = nil
    }
    
    func sendCommand(action: String, params: [String: String] = [:]) {
        guard isConnected, let p = targetPeripheral, let rx = rxCharacteristic else {
            print("🔵 [WatchBLE] Gönderilemedi (Bağlı değil)")
            return
        }
        
        var payloadDict: [String: Any] = ["action": action]
        for (k, v) in params { payloadDict[k] = v }
        
        guard let data = try? JSONSerialization.data(withJSONObject: payloadDict) else { return }
        let type: CBCharacteristicWriteType = rx.properties.contains(.writeWithoutResponse) ? .withoutResponse : .withResponse
        p.writeValue(data, for: rx, type: type)
        print("🔵 [WatchBLE] Komut gönderildi: \(action)")
    }
    
    // MARK: - CBCentralManagerDelegate
    func centralManagerDidUpdateState(_ central: CBCentralManager) {
        isBluetoothPoweredOn = (central.state == .poweredOn)
        print("🔵 [WatchBLE] State: \(central.state.rawValue)")
        if central.state == .poweredOn {
            startScan()
        } else {
            isConnected = false
            connectedDeviceName = nil
        }
    }
    
    func centralManager(_ central: CBCentralManager, didDiscover peripheral: CBPeripheral, advertisementData: [String : Any], rssi RSSI: NSNumber) {
        print("🔵 [WatchBLE] Cihaz bulundu: \(peripheral.name ?? "Bilinmeyen") (RSSI: \(RSSI))")
        targetPeripheral = peripheral
        central.stopScan()
        central.connect(peripheral, options: nil)
    }
    
    func centralManager(_ central: CBCentralManager, didConnect peripheral: CBPeripheral) {
        print("✅ [WatchBLE] Bağlandı: \(peripheral.name ?? "Bilinmeyen")")
        isConnected = true
        connectedDeviceName = peripheral.name ?? "Android Telefon (BLE)"
        WKInterfaceDevice.current().play(.success)
        
        peripheral.delegate = self
        peripheral.discoverServices([Self.serviceUUID])
    }
    
    func centralManager(_ central: CBCentralManager, didDisconnectPeripheral peripheral: CBPeripheral, error: Error?) {
        print("⚠️ [WatchBLE] Bağlantı koptu. Yeniden taranıyor...")
        isConnected = false
        connectedDeviceName = nil
        targetPeripheral = nil
        rxCharacteristic = nil
        txCharacteristic = nil
        
        // Scan again after 3 seconds
        DispatchQueue.main.asyncAfter(deadline: .now() + 3.0) { [weak self] in
            self?.startScan()
        }
    }
    
    // MARK: - CBPeripheralDelegate
    func peripheral(_ peripheral: CBPeripheral, didDiscoverServices error: Error?) {
        guard let services = peripheral.services else { return }
        for service in services where service.uuid == Self.serviceUUID {
            peripheral.discoverCharacteristics([Self.rxCharUUID, Self.txCharUUID], for: service)
        }
    }
    
    func peripheral(_ peripheral: CBPeripheral, didDiscoverCharacteristicsFor service: CBService, error: Error?) {
        guard let characteristics = service.characteristics else { return }
        for char in characteristics {
            if char.uuid == Self.rxCharUUID {
                self.rxCharacteristic = char
                print("🔵 [WatchBLE] RX Characteristic hazır (Yazılabilir)")
            } else if char.uuid == Self.txCharUUID {
                self.txCharacteristic = char
                peripheral.setNotifyValue(true, for: char)
                print("🔵 [WatchBLE] TX Characteristic hazır (Dinleniyor)")
            }
        }
    }
    
    func peripheral(_ peripheral: CBPeripheral, didUpdateValueFor characteristic: CBCharacteristic, error: Error?) {
        guard let data = characteristic.value else { return }
        // Received update over BLE from Phone
        if let jsonStr = String(data: data, encoding: .utf8) {
            print("📩 [WatchBLE] Alındı: \(jsonStr)")
            DispatchQueue.main.async {
                WatchSyncService.shared.handleIncomingBLEPayload(data)
            }
        }
    }
}
