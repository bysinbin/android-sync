# Android-Mac Sync (Go + Kotlin)

Android telefonunuz ile Mac arasında yerel ağ (Wi-Fi) üzerinden **iOS-macOS ekosistemi benzeri** kesintisiz bağlantı sağlayan özel sistem.

---

## 🌟 Temel Yetenekler

1. **🔔 Bildirim Senkronizasyonu:**
   - Android'e gelen tüm uygulama bildirimleri (WhatsApp, SMS, Bankacılık vb.) anında yerel macOS Bildirim Merkezi'nde sesli olarak gösterilir.
2. **📞 Gelen Arama Algılama:**
   - Telefon çaldığında Mac'te arayanın adı ve numarasıyla bildirim çıkar.
   - Mac'te o sırada çalan müzik/medya (Apple Music, Spotify vb.) arama boyunca **otomatik duraklatılır (pause)**.
3. **🎵 Mac Medya & Ses Kontrolü:**
   - Telefon ekranından tek tıkla Mac'teki müziği Oynat/Duraklat (⏯), Önceki (⏮), Sonraki (⏭) yapabilir; Mac'in sistem sesini artırıp (🔊) azaltabilirsiniz (🔉).
4. **📋 Ortak Pano (Shared Clipboard):**
   - Telefondan kopyalanan metin anında Mac panosuna aktarılır (`Cmd + V`).
   - Mac'te kopyalanan metin telefona aktarılır.
5. **⚡ Otomatik Ağ Keşfi (UDP Discovery):**
   - İki cihaz aynı Wi-Fi ağındayken IP adresi yazmanıza gerek kalmaz; UDP broadcast ile birbirlerini otomatik bulur ve WebSocket üzerinden bağlanır.

---

## 🛠️ Nasıl Çalıştırılır?

### 1. Mac Daemon'ını Başlatma
```bash
./start-mac.sh
```
*(Veya `cd mac-daemon && ./mac-sync`)*

Daemon başladığında:
- Port `42425` (UDP) üzerinden LAN'da keşif yayını yapar.
- Port `42424` (TCP/WS) üzerinden telefonla canlı veri hattı kurar.

### 2. Android Uygulamasını Yükleme
APK dosyanız derlendi ve doğrudan telefonunuzun **İndirilenler (Download)** klasörüne aktarıldı:
- **Dosya Konumu:** Telefonunuzda *Dosya Yöneticisi -> İndirilenler -> `MacSync.apk`*
- Açıp **Yükle (Install)** butonuna basmanız yeterlidir.

> [!TIP]
> Xiaomi / Redmi cihazlarda USB'den direkt kurmak isterseniz:
> *Ayarlar -> Ek Ayarlar -> Geliştirici Seçenekleri -> "USB üzerinden yükle"* seçeneğini açıp Mac terminalinden `~/Library/Android/sdk/platform-tools/adb install -r android-app/app/build/outputs/apk/debug/app-debug.apk` çalıştırabilirsiniz.

---

## 📱 Uygulama İlk Kurulumu (İzinler)
Uygulamayı ilk açtığınızda ekranda 2 izin butonu göreceksiniz:
1. **Bildirim Erişimi:** Android Ayarlarından "Mac Sync" için bildirim okuma iznini etkinleştirin.
2. **Çağrı Erişimi:** Gelen aramaları tespit edebilmek için telefon durumu iznini onaylayın.

İzinler verildikten sonra Mac ile otomatik eşleşme sağlanacak ve ekranda **"Mac'e Bağlandı 🟢"** rozeti belirecektir.


## 3. Yapılacaklar
1. **arama yanıtlamada kabul etme**
2. **touchbar da sürekli icon**
3. **mac üzerinden arama konuşma**
4. **mac üzerinden mesajlar**
5. **mac e güzel bir arayüz**