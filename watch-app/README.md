# ⌚ MacSync Apple Watch Uygulaması (watchOS)

MacSync Apple Watch uygulaması; Mac bilgisayarınız ve Android telefonunuz ile doğrudan yerel Wi-Fi ağı ve Bluetooth Low Energy (BLE) üzerinden senkronize olan, **bağımsız (Standalone)** bir watchOS SwiftUI uygulamasıdır. iPhone'a ihtiyaç duymadan çalışır.

---

## 🚀 Başlıca Özellikler

### 1. 🎵 Gelişmiş Medya & Parça Kontrolü
- **Mac & Telefon Geçişi:** Üstteki tek dokunuşluk butonlarla kontrol edilen cihazı (💻 Mac veya 📱 Android) anında değiştirin.
- **Canlı Albüm Kapağı (Artwork):** Şarkının albüm kapağı otomatik yüklenir veya çalan müziğin ritmine göre dinamik vinil animasyonu görüntülenir.
- **Digital Crown İkili Mod (Ses vs Süre):**
  - **Ses Modu:** Crown çevrildiğinde her seviye değişiminde mikro titreşimle (haptic `.click`) cihazın sesini hassasça ayarlar.
  - **Süre (Seek) Modu:** Crown çevrildiğinde veya zaman çizelgesine dokunulduğunda şarkıyı istenen saniyeye atlatır, kalan/geçen süreyi ve `+15 sn` gibi delta göstergesini canlı sunar.

### 2. 🔋 Cihaz & Pil İzleme
- **Android Telefon Durumu:** Telefonun anlık şarj yüzdesi, şarj olup olmadığı ve bağlantı durumu.
- **Telefonu Çaldır (Find My Phone):** Saatten tek tıkla Android telefonunuzda yüksek sesli alarm çaldırıp durdurabilirsiniz.
- **Zil Modları:** Telefonunuzu saat üzerinden **Normal**, **Titreşim** veya **Sessiz** moda alabilirsiniz.
- **Mac Durumu:** Mac'in bağlantı durumu ve yerel IP adresi.

### 3. 📡 Dinamik Alt Ağ & Bonjour (ZeroConf) Keşfi
- **Sabit IP Zorunluluğu Yok:** Saat açıldığında yerel Wi-Fi arayüzünden (`en0`) alt ağı dinamik olarak analiz eder (`192.168.254.x`, `192.168.1.x`, `172.20.10.x` vb.).
- **Bonjour / mDNS (`_macsync._tcp`):** Hem Mac hem Android cihazları sıfır yapılandırmayla otomatik keşfeder.
- **Son Başarılı IP Hafızası:** Bir kez bulunan cihaz IP'si kalıcı olarak saklanır; ağ değişse bile anında tekrar bağlanır.

### 4. 🔵 Bluetooth Low Energy (BLE) Köprüsü & Çevrimdışı Mod
- Wi-Fi ağı olmayan ortamlarda (sokakta, yürüyüşte, arabada), saat CoreBluetooth ile doğrudan Android telefona bağlanır.
- Çevrimdışıyken bile medya kontrolleri (Oynat/Durdur/Ses) ve arama bildirimleri BLE üzerinden akmaya devam eder.
- Üst çubukta "BLE 🔵" göstergesi ile bağlantı türü canlı izlenir.

### 5. ⚡ Akıllı Uyarlanabilir Polling (Pil Optimizasyonu)
- Sabit aralıklı agresif HTTP sorguları yerine durum bazlı uyarlanabilir sorgulama:
  - Müzik çalarken / arama gelirken: **1.0 – 1.5 sn**
  - Cihaz boştayken: **4.0 sn** (İşlemci ve Wi-Fi uykuya geçerek pil ömrünü 3 kat uzatır)
  - Bağlantı koptuğunda: **Üstel geri çekilme (Exponential backoff)**

### 6. 📞 Ritmik Nabız Titreşimli Gelen Arama Uyarısı
- Android telefonunuza çağrı geldiğinde tam ekran kart açılır.
- Apple Watch'un Taptic Engine motoru arama süresince ritmik nabız gibi titreşir (`.notification` döngüsü).
- Saatten tek tıkla **Cevapla** veya **Reddet** yapılabilir.

### 7. 🔔 Bildirim Merkezi & Hızlı Eylemler
- WhatsApp, Telegram, SMS ve sistem bildirimleri anında saat ekranına kart olarak düşer.
- Bildirim kartı üzerinden **"Okundu"** veya tek dokunuşla **"Tamam 👍"** gibi hazır yanıt aksiyonları tetiklenebilir.

---

## 🛠️ Apple Watch'a Yükleme (Deployment) Rehberi

### 1. Ön Hazırlık
- Mac'inizde **Xcode** yüklü olmalıdır.
- Apple Watch ve Mac'inizin **aynı Wi-Fi ağına** bağlı olduğundan emin olun.
- Uygulama yüklenirken saatin **manyetik şarj aletine takılı olması ve kilidinin açık tutulması** bağlantı zaman aşımlarını engeller.

### 2. Apple Watch'ta Geliştirici Modunu Açma
1. Saatinizde **Ayarlar > Gizlilik ve Güvenlik > Geliştirici Modu (Developer Mode)** bölümüne gidin.
2. Geliştirici Modu'nu açın ve saatinizi yeniden başlatın. Açılışta *"Geliştirici Modunu Aç"* uyarısını onaylayın.

### 3. Xcode ile Projeyi Derleme ve Saate Aktarma
1. Terminalden projeyi açın:
   ```bash
   open watch-app/WatchSync.xcodeproj
   ```
2. Xcode üst menüsünden hedef cihaz olarak **"ferit Apple Watch’u"** (veya eşleşmiş saatinizi) seçin.
3. **Signing & Capabilities** sekmesinde kendi ücretsiz Apple ID'nizi (*Personal Team*) seçin.
4. **Run (Play ▶)** butonuna basın. Uygulama otomatik olarak derlenecek ve kablosuz olarak Apple Watch'unuza yüklenecektir!
