# Android Sync (Windows & macOS)

Android telefonunuz ile **Windows** veya **macOS** bilgisayarınız arasında yerel ağ (Wi-Fi) üzerinden **Apple & Samsung ekosistemleri benzeri** kesintisiz bağlantı sağlayan sistem.

---

## 🌟 Temel Yetenekler

1. **🔔 Bildirim Senkronizasyonu:**
   - Android'e gelen tüm uygulama bildirimleri (WhatsApp, SMS, Bankacılık vb.) anında yerel **Windows 10/11 Toast Bildirimlerinde** veya **macOS Bildirim Merkezi'nde** sesli olarak gösterilir.
2. **📞 Gelen Arama Algılama & Otomatik Duraklatma:**
   - Telefon çaldığında bilgisayarınızda arayanın adı ve numarasıyla bildirim çıkar.
   - Bilgisayarda o sırada çalan müzik/medya (Spotify, YouTube, Chrome, VLC vb.) arama boyunca **otomatik duraklatılır (pause)**.
3. **🎵 PC & Telefon Medya & Ses Kontrolü:**
   - Telefondan bilgisayardaki müziği Oynat/Duraklat (⏯), Önceki (⏮), Sonraki (⏭) yapabilir; sistem sesini artırıp (🔊) azaltabilirsiniz (🔉).
   - Bilgisayardan da telefonun medyasını ve sesini kontrol edebilirsiniz.
4. **📋 Evrensel Ortak Pano (Mesh Clipboard):**
   - Telefondan kopyalanan metin anında bağlı tüm bilgisayarların panosuna aktarılır (`Ctrl + V` veya `Cmd + V`).
   - PC-1'den kopyalanan metin telefon üzerinden PC-2'ye de taşınır (Cross-Device Clipboard Mesh).
5. **⚡ Otomatik Ağ Keşfi (UDP Discovery):**
   - İki cihaz aynı Wi-Fi ağındayken IP adresi yazmanıza gerek kalmaz; UDP broadcast ile birbirlerini otomatik bulur ve WebSocket üzerinden bağlanır.
6. **💻 Çoklu Bilgisayar (Multi-PC) Eşzamanlı Bağlantı:**
   - Telefonunuz aynı anda birden fazla bilgisayara (örn. hem Windows masaüstünüz hem de dizüstü Mac/Windows PC) bağlanabilir.
   - Bildirimler ve çağrılar tüm bilgisayarlara aynı anda dağıtılır.
7. **🌐 Modern Web Kontrol Paneli & Görev Çubuğu (System Tray):**
   - Windows Görev Çubuğunda durum ikonu (Bağlantı & Pil göstergesi).
   - `http://localhost:42424` adresinde modern, cam efektli (Glassmorphic) kontrol paneli.
   - "Telefonumu Bul (Çaldır)" butonu ile telefon sessizde olsa dahi yüksek sesle çaldırma.

---

## 🛠️ Windows'ta Nasıl Çalıştırılır?

### 1. Servisi Başlatma
Proje kök dizinindeki `start-windows.bat` dosyasına çift tıklayın veya terminalden çalıştırın:
```cmd
start-windows.bat
```
*(PowerShell alternatifi: `.\start-windows.ps1`)*

Bu komut:
- `windows-sync.exe` mevcut değilse Go ile otomatik derler.
- Arka planda UDP keşif servisini (Port 42425) ve WebSocket sunucusunu (Port 42424) başlatır.
- Windows Görev Çubuğu'na (System Tray) durum ikonunu yerleştirir.
- `http://localhost:42424` adresinde web kontrol panelini açar.

### 2. Bilgisayar Başlangıcına Ekleme (Opsiyonel)
Bilgisayarınız her açıldığında otomatik olarak arka planda başlaması için:
- `install-windows-service.bat` dosyasına çift tıklayın.
*(Kaldırmak için `uninstall-windows-service.bat` dosyasını çalıştırabilirsiniz).*

---

## 🍏 macOS'ta Nasıl Çalıştırılır?

```bash
./start-mac.sh
```
*(Veya menü çubuğu uygulamasını derlemek için `./build-app.sh`)*

---

## 📱 Android Uygulamasını Yükleme

1. Bilgisayarınızda servis çalışırken tarayıcınızdan `http://localhost:42424` kontrol panelini açın.
2. Telefonunuzdan aynı Wi-Fi ağında bilgisayarınızın yerel IP adresine gidip (örn. `http://192.168.1.x:42424/download`) **AndroidSync.apk** dosyasını indirin ve kurun.
3. Uygulamayı açtığınızda **Bildirim Erişimi** ve **Telefon Durumu** izinlerini onaylayın.
4. Cihazlar aynı Wi-Fi ağında birbirlerini otomatik olarak algılayıp bağlanacaktır.

---

## 📂 Proje Yapısı

```
android-sync/
├── windows-daemon/             # Windows için yerel Go daemon motoru
│   ├── cmd/windows-sync/       # Windows ana giriş noktası
│   └── internal/
│       ├── discovery/          # UDP Broadcast & Beacon keşif servisi
│       ├── protocol/           # WebSocket mesaj protokolü
│       ├── server/             # HTTP, WebSocket & Web Dashboard sunucusu
│       └── windows/            # Win32 Pano, Medya Tuşları, Toast & System Tray
├── windows-sync.exe            # Derlenmiş yerel Windows çalıştırılabilir dosyası
├── start-windows.bat           # Windows tek tıkla başlatma betiği
├── build-windows.bat           # Windows tek tıkla derleme betiği
├── install-windows-service.bat # Windows başlangıç kurulum betiği
├── uninstall-windows-service.bat
├── mac-daemon/                 # macOS Go arka plan servisi
├── mac-app/                    # macOS Swift Menü Çubuğu uygulaması
├── android-app/                # Android istemci uygulaması (Kotlin)
└── README.md
```


## 3. Özellik & Entegrasyon Durumu

### ✅ Windows & Android (Tamamlandı):
1. **Arama Yanıtlama & Reddetme:** Gelen aramaları PC'den anında kabul etme (`ANSWER`) veya reddetme (`REJECT`), ayrıca tuş takımı ile numara çevirip arama başlatma.
2. **Arama Konuşma:** Bluetooth Eller Serbest (Handsfree/HFP) veya Hoparlör (Speakerphone) ses yönlendirme ve arama içi denetimler.
3. **Mesajları Okuyup Mesaj Gönderme:** 2 sütunlu SMS mesajlaşma paneli, gelen/giden mesajların gerçek zamanlı senkronizasyonu ve Windows'tan SMS gönderme.
4. **Güzel Bir Arayüz (Windows Fluent & Android Dark):** Hem Windows masaüstü web paneli hem de Android uygulaması modern koyu tema ve kart tabanlı tasarımla baştan yenilendi.
5. **🔒 Güvenlik & 6 Haneli PIN Eşleştirme:** Yetkisiz ağ bağlantılarına karşı uçtan uca doğrulama; yeni cihazlar bağlandığında ekranda 6 haneli kod onayı istenir, kriptografik token ile güvenle eşleştirilir (`daemon_config.json`).
6. **💻 Gelişmiş Çoklu Cihaz (Multi-PC) Yönetimi:**
   - Telefondan bağlı Windows ve Mac bilgisayarları kartlar halinde ayrı ayrı listeleme ve durumlarını görme (🪟 Windows / 🍏 Mac simgeleriyle).
   - Her bilgisayar için ayrı ayrı izin anahtarları: Pano Paylaşımı, Arama Yanıtlama, SMS Erişimi, Medya Kontrolü.
   - Çapraz Pano (Mesh Forwarding) ve Tüm Cihazlarda Çaldırma yönlendirme tercihleri.
   - Tek tıkla eşleşmeyi kaldırma (unpair) veya yeniden bağlanma.


---

### 🍏 macOS (Hazırlananlar & Mac'e Geçildiğinde Yapılacaklar):

#### 🟢 Hazırlanan & Tamamlanan Mac Altyapısı:
1. **🔒 Güvenlik & 6 Haneli PIN Eşleştirme (Mac Daemon):**
   - `mac-daemon` Windows ile 100% protokoler uyumlu hale getirildi (`EventAuthRequest`, `EventAuthResponse`, `EventPairConfirm`, `EventUnpair`).
   - İlk bağlantıda Android telefona `auth_request` (OS: `macos`) gönderilir, telefon ekranında 6 haneli kod onayı istenir.
   - Onaylanan token `~/Library/Application Support/MacSync/daemon_config.json` içine güvenle kaydedilir.
   - `/pair/reset` endpoint'i ile eşleşme tek tıkla sıfırlanabilir.
2. **📞 Mac Arama Kontrol Uç Noktaları (`/call/action`):**
   - Mac üzerinden `ANSWER` (Kabul Et), `REJECT` (Reddet), `HANGUP` (Sonlandır), `DIAL` (Ara), `SET_SPEAKER` ve `SET_MUTE` komutları tamamlandı.
   - Gelen arama anında Mac medyası otomatik duraklatılır (`macos.ExecuteMediaAction("PAUSE")`).
   - Arama geldiğinde hem yerel sistem bildirimi hem de Mac Menü Çubuğu/TouchBar uygulamasına IPC (`http://127.0.0.1:42426/call`) iletilir.
3. **💬 Mac SMS Hub API'leri (`/sms/list`, `/sms/send`, `/sms/sync`):**
   - Telefondaki tüm SMS mesajları Mac'e çekilir (`/sms/sync`).
   - Mac klavyesinden doğrudan telefon SIM hattı üzerinden SMS gönderilir (`/sms/send`).
   - Yeni SMS geldiğinde Mac bildirim sesiyle ekranda görünür ve TouchBar/Menü çubuğuna IPC ile iletilir.
4. **🎨 macOS İçin Apple Tasarımlı Web Kontrol Paneli (`http://localhost:42424`):**
   - San Francisco / Plus Jakarta Sans tipografisi, şık koyu cam efektli kartlar.
   - Üst barda 🔒 Güvenlik & PIN durumu rozeti, canlı çağrı banner'ı (Yanıtla / Reddet butonları), 2 sütunlu SMS mesajlaşma arayüzü, numara çevirici ve Apple Music / Spotify medya kontrolleri.
5. **💻 TouchBar Entegrasyonu Güncellendi (`mac-app/src/main.swift`):**
   - Gelen arama anında TouchBar'da doğrudan **📞 Yanıtla** (yeşil) ve **✕ Reddet** (kırmızı) butonları eklendi.
   - SMS geldiğinde macOS bildirim merkezine başlık ve mesaj metni düşecek şekilde IPC güncellendi.
6. **🚀 Tek Tıkla Başlatma & Durdurma Betikleri:**
   - `./start-mac.sh`: İkili dosyayı otomatik belirler (Apple Silicon `arm64` veya Intel `amd64`), arka planda başlatır ve Safari'de paneli açar.
   - `./stop-mac.sh`: Arka plan servislerini temiz bir şekilde sonlandırır.
7. **⚙️ Derlenmiş macOS İkili Dosyaları:**
   - `mac-daemon/mac-sync-arm64` (Apple Silicon M1/M2/M3/M4)
   - `mac-daemon/mac-sync-amd64` (Intel Mac)
8. **🔔 Mac Bildirim Dinleyicisi (Mac ➡️ Telefon Çift Yönlü Bildirim):**
   - macOS Bildirim Merkezi veritabanını (`db2/db`) gerçek zamanlı dinleyen SQLite/Plist motoru entegre edildi.
   - Mac'e gelen sistem ve uygulama bildirimleri (WhatsApp, Telegram, Mail, Mesajlar, Takvim vb.) anında Android telefona `pc_notification` protokolü ile iletilir.
9. **🎵 Bağımsız Çift Medya & Canlı Süre Kontrolü (Dual Media Seek Bar):**
   - Mac kontrol panelinde hem **🍏 Bu Mac** hem de **📱 Telefon** medyası ayrı kartlarda bağımsız olarak takip edilir.
   - Her iki medya için canlı süre ilerleme çubuğu (Seek Bar), süre sayacı (`01:24 / 03:45`), ±15 saniye hızlı ileri/geri sarma butonları ve AppleScript/Chrome/Safari üzerinden hassas konum kontrolü sağlandı.

---

#### 📋 Mac'e Geçildiğinde Yapılacak Adımlar (Çalıştırma & Doğrulama):

1. **Servisi Başlatma:**
   ```bash
   chmod +x start-mac.sh stop-mac.sh
   ./start-mac.sh
   ```
2. **Telefondan Eşleştirme Onayı:**
   - Telefonunuzda AndroidSync uygulamasını açın veya otomatik keşifle bağlanmasını bekleyin.
   - Bilgisayar ekranında veya `http://localhost:42424` panelindeki 6 haneli PIN kodu telefondaki diyalogla eşleştiğinde **"Eşleştir ve İzin Ver"** butonuna basın.
   - Kart üzerindeki izinleri (Pano, Arama, SMS, Medya) dilediğiniz gibi açıp kapatın.
3. **Arama Konuşma (Ses Köprüsü) Doğrulaması:**
   - Mac Sistem Ayarları -> Bluetooth bölümünden Android telefonunuzu Mac ile Bluetooth Handsfree (HFP) olarak eşleştirin.
   - Böylece Mac'ten "Yanıtla" dediğinizde arama Mac'in mikrofon ve hoparlörü üzerinden eller serbest konuşulabilir.
4. **Çapraz Cihaz (Mesh Clipboard) Testi:**
   - Hem Windows PC hem Mac açıkken; Mac'te `Cmd + C` yapın -> Windows PC'de `Ctrl + V` ile yapıştırın!
5. **(Opsiyonel) Yerel Swift Menü Çubuğu Uygulamasını Derleme:**
   ```bash
   cd mac-app
   ./build.sh   # Veya swiftc -o MacSync src/main.swift
   open MacSync.app
   ```