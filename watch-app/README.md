# ⌚ MacSync Apple Watch Uygulaması (watchOS)

MacSync Apple Watch uygulaması, Mac bilgisayarınız ve Android telefonunuz ile doğrudan yerel Wi-Fi ağı üzerinden senkronize olan bağımsız (Standalone) bir watchOS SwiftUI uygulamasıdır. iPhone'a bağımlı olmadan bağımsız çalışabilir.

---

## 🚀 Özellikler

### 1. 🎵 Medya & Parça Kontrolü
- **Mac & Telefon Geçişi:** Üstteki tek dokunuşluk butonlarla kontrol edilen cihazı (💻 Mac veya 📱 Android) anında değiştirin.
- **Şarkı Bilgileri:** Şarkı adı, sanatçı ve albüm bilgileri anlık olarak saat ekranında akar.
- **Parça İlerleme Çubuğu:** Çalan şarkının konumu, kalan/toplam süre (`01:24 / 03:45`) ve dokunarak istenen saniyeye sarma (seek) özelliği.
- **Digital Crown (Saat Tekeri) ile Ses Kontrolü:** Saatin tekerini çevirdiğinizde Mac veya telefonun ses düzeyi anında değişir ve parmağınızda gerçekçi titreşimli (haptic) geri bildirim hissettirir.

### 2. 🔋 Cihaz & Pil İzleme
- **Android Telefon Durumu:** Telefonun anlık şarj yüzdesi, şarj olup olmadığı ve bağlantı durumu.
- **Telefonu Çaldır (Find My Phone):** Saatten tek tıkla Android telefonunuzda yüksek sesli alarm çaldırıp durdurabilirsiniz.
- **Zil Modları:** Telefonunuzu saat üzerinden **Normal**, **Titreşim** veya **Sessiz** moda alabilirsiniz.
- **Mac Durumu:** Mac'in bağlantı durumu ve yerel IP adresi.

### 3. ⚡ Hızlı Eylemler (Uzaktan Kumanda)
- **Mac'i Kilitle:** Tek tıkla macOS oturumunu kilitler.
- **Mac'i Uyut:** Mac'inizi uyku moduna alır.
- **Mac Sesi Kapat:** Sesi tek tıkla tamamen keser.
- **Durumu Yenile:** Tüm cihaz durumlarını tek dokunuşla senkronize eder.

### 4. 📞 Gelen Arama Bildirimi
- Android telefonunuza çağrı geldiğinde Apple Watch titreşir ve ekranda arayanın adı/numarası ile tam ekran kart açılır.
- Saatinizden tek dokunuşla **Cevapla** veya **Reddet** yapabilirsiniz.

### 5. 📡 Otomatik Ağ Keşfi (UDP & WebSocket)
- `mac-daemon` tarafından yayınlanan UDP paketlerini (Port: 42425) otomatik dinler.
- "Ağda Mac Ara" butonuyla Mac'inizi otomatik bulur veya Ayarlar sekmesinden elle IP/Port belirleyebilirsiniz.

---

## 🛠️ Apple Watch'a Yükleme (Deployment) Rehberi

Apple ekosisteminde bir Apple Watch'a doğrudan uygulama yüklemek için Apple'ın gereksinimleri şunlardır:

### 1. Ön Hazırlık (Disk Alanı & Xcode)
- watchOS uygulamaları Mac üzerinde **Xcode** ile derlenir.
- Mac'inizde şu anda ~7.7 GB boş alan bulunmaktadır. Xcode kurulumu için diskte yaklaşık **25–30 GB** yer açılması gerekmektedir (İndirilenler, Çöp Kutusu, kullanmadığınız büyük dosyalar veya video projeleri harici diske aktarılabilir).
- Mac App Store'dan veya [developer.apple.com/download](https://developer.apple.com/download/) adresinden **Xcode**'u kurun.

### 2. Apple Watch'ta Geliştirici Modunu Açma
1. Saatinizde **Ayarlar > Gizlilik ve Güvenlik > Geliştirici Modu (Developer Mode)** bölümüne gidin.
2. Geliştirici Modu'nu açın ve saatinizi yeniden başlatın.
3. *(Eğer saat iPhone ile eşliyse, iPhone'da da Ayarlar > Gizlilik ve Güvenlik > Geliştirici Modu'nu açın).*

### 3. Xcode ile Projeyi Derleme ve Saate Aktarma
1. Mac'inizde bu klasördeki projeyi açın:
   ```bash
   open watch-app/WatchSync.xcodeproj
   ```
2. Xcode üst menüsünden hedef cihaz olarak **"ferit Apple Watch’u"** (veya bağlı saatinizi) seçin.
3. **Signing & Capabilities** sekmesinde kendi ücretsiz Apple ID'nizi (*Personal Team*) seçin.
4. **Run (Play ▶)** butonuna basın.
5. Uygulama otomatik olarak derlenecek ve kablosuz olarak Apple Watch'unuza yüklenecektir!
