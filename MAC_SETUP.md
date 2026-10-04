# 🍏 macOS Entegrasyon ve Kurulum Kılavuzu (Android-Mac Sync)

Bu kılavuz, Windows üzerinde geliştirilen **6 haneli PIN eşleştirmeli güvenlik**, **arama yanıtlama & reddetme**, **SMS okuma & gönderme**, **TouchBar denetimleri** ve **Windows-Mac-Android çoklu cihaz pano ağı (mesh clipboard)** sistemini Mac bilgisayarınızda nasıl çalıştıracağınızı ve kullanacağınızı adım adım açıklar.

---

## 🏗️ 1. Mimari ve Hazırlanan Altyapı

| Bileşen | Dosya / Dizin | Durum | Açıklama |
| :--- | :--- | :--- | :--- |
| **Mac Daemon (Motor)** | `mac-daemon/cmd/mac-sync/` | ✅ Hazır & Derlendi | UDP keşfi, WebSocket, 6 haneli PIN yetkilendirmesi, SMS hub ve çağrı yönlendirme servisi. |
| **Apple Silicon İkili** | `mac-daemon/mac-sync-arm64` | ✅ Derlendi | M1, M2, M3, M4 işlemcili Mac'ler için yerel binary. |
| **Intel Mac İkili** | `mac-daemon/mac-sync-amd64` | ✅ Derlendi | Intel Core i5/i7/i9 işlemcili Mac'ler için yerel binary. |
| **macOS Web Paneli** | `http://localhost:42424` | ✅ Hazır | Apple San Francisco / Plus Jakarta Sans fontlu, koyu cam efektli çağrı, SMS ve medya paneli. |
| **TouchBar & Menü Çubuğu** | `mac-app/src/main.swift` | ✅ Güncellendi | TouchBar'da doğrudan çağrı Yanıtla (Yeşil) / Reddet (Kırmızı) butonları ve SMS bildirimleri. |
| **Başlatma / Durdurma** | `start-mac.sh` / `stop-mac.sh` | ✅ Hazır | Tek tıkla servisi ayağa kaldıran ve kapatan betikler. |

---

## 🚀 2. Mac'te Çalıştırma Adımları

Mac bilgisayarınızda bir terminal açın ve proje dizinine gidin:

```bash
cd /path/to/android-sync
chmod +x start-mac.sh stop-mac.sh
./start-mac.sh
```

`./start-mac.sh` betiği:
1. Mac'inizin mimarisini otomatik tespit eder (ARM64 veya Intel) ve uygun ikili dosyayı seçer.
2. Arka planda `mac-sync` daemon'unu başlatır (Port 42424 ve UDP 42425).
3. Safari veya varsayılan tarayıcınızda `http://localhost:42424` kontrol panelini açar.
4. Ekrana o oturuma özel üretilen rastgele 6 haneli **Eşleştirme PIN kodunu** yazar.

---

## 🔒 3. Güvenli Eşleştirme (6 Haneli PIN)

1. Telefonunuzda **AndroidSync** uygulamasını açın (telefonunuz ve Mac aynı Wi-Fi ağında olmalıdır).
2. Telefonunuz Mac'i otomatik olarak keşfedecek ve ekranda **"Yeni Bilgisayar Eşleştirme İsteği"** penceresi açılacaktır.
3. Mac ekranında (veya `http://localhost:42424` panelinin üst barında) görünen 6 haneli kodu kontrol edin.
4. Telefondan **"Eşleştir ve İzin Ver"** butonuna dokunun.
5. Kriptografik token Mac'te `~/Library/Application Support/MacSync/daemon_config.json` dosyasına kaydedilir ve sonraki bağlantılarda tekrar kod sormadan otomatik bağlanır.

> **İzin Yönetimi:** Telefon uygulamasındaki **🍏 Mac** kartı üzerinden Pano, Arama, SMS ve Medya izinlerini istediğiniz an bağımsız olarak açıp kapatabilirsiniz.

---

## 📞 4. Arama Yanıtlama, Konuşma ve TouchBar

### Arama Geldiğinde:
- macOS Bildirim Merkezi'nde arayanın adı ve numarası sesli olarak gösterilir.
- Mac'te o sırada çalan müzik/video otomatik olarak duraklatılır (PAUSE).
- **TouchBar'da:** Doğrudan **📞 Yanıtla** (Yeşil) ve **✕ Reddet** (Kırmızı) butonları belirir.
- **Web Panelinde:** Canlı çağrı kartı açılır. Tek tıkla "Yanıtla" veya "Reddet" diyebilirsiniz.

### Ses Köprüsü (Eller Serbest Konuşma):
1. Mac'inizde **Sistem Ayarları -> Bluetooth** menüsüne gidin.
2. Android telefonunuzu bulun ve Bluetooth üzerinden eşleştirin (Hands-Free Audio / HFP profili).
3. Artık Mac'ten "Yanıtla" butonuna bastığınızda çağrının sesi Mac'in hoparlörlerine, sizin sesiniz ise Mac'in dahili stüdyo mikrofonuna yönlendirilir.

---

## 💬 5. SMS Mesajlarını Okuma ve Mac'ten Gönderme

1. Tarayıcınızda `http://localhost:42424` adresini açın veya sol menüden **Mesajlar (SMS)** sekmesine geçin.
2. Telefondaki tüm mesaj dizileri sol sütunda listelenir.
3. Bir kişiye tıkladığınızda konuşma geçmişi sağda açılır.
4. Alttaki kutuya Mac klavyenizle mesajınızı yazıp **Enter** veya **Gönder** tuşuna bastığınızda, mesaj doğrudan telefonunuzun SIM kartı üzerinden SMS olarak iletilir.
5. Yeni bir SMS geldiğinde Mac ekranında bildirim çıkar.

---

## 🌐 6. Çapraz Cihaz (Windows ⟷ Mac ⟷ Android) Ekosistemi

Hem Windows masaüstü bilgisayarınız hem de Mac dizüstü bilgisayarınız açıkken:
- **Çapraz Pano (Mesh Clipboard):**
  - Mac'te `Cmd + C` ile bir metin kopyaladığınızda metin telefon üzerinden anında Windows PC'nize iletilir ve `Ctrl + V` ile yapıştırabilirsiniz!
  - Windows'ta kopyaladığınız bir metin de anında Mac panosuna taşınır.
- **Arama Çaldırma:**
  - Telefon çaldığında hem Mac hem Windows ekranında aynı anda bildirim ve çağrı kartı belirir. Hangisinden kabul ederseniz diğeri otomatik kapanır.

---

## 🛑 7. Servisleri Durdurma

İşiniz bittiğinde arka plan servislerini tek bir komutla temizce kapatabilirsiniz:
```bash
./stop-mac.sh
```
