# Topluyo .direct — Gerçek Zamanlı WebSocket Sinyal Sunucusu

Topluyo platformu için yüksek performanslı, Go tabanlı WebSocket sinyal sunucusu. Kullanıcıları **grup** ve **kanal** bazında yönetir; hedefli broadcast mesajları ile gerçek zamanlı iletişim sağlar.

## 📋 İçindekiler

- [Mimari](#-mimari)
- [Teknoloji Yığını](#-teknoloji-yığını)
- [Kurulum](#-kurulum)
- [Kullanım](#-kullanım)
- [WebSocket Protokolü](#-websocket-protokolü)
- [HTTP API](#-http-api)
- [Veritabanı](#-veritabanı)
- [Test Modu](#-test-modu)
- [Stres Testi](#-stres-testi)
- [Proje Yapısı](#-proje-yapısı)
- [Lisans](#-lisans)

---

## 🏗 Mimari

```
                                ┌──────────────────────────────┐
 Kullanıcı (Browser/App)       │    .direct Sunucusu (Go)     │
 ──────────────────────         │                              │
  ws://host/!direct ──────────► │  WebSocket Handler           │
                                │    ├─ Auth (token / IP hash) │
  HTTP Signal ─────────────────►│    ├─ Grup/Kanal yönetimi    │
  GET /!direct-signal           │    └─ Broadcast motoru       │
                                │                              │
                                │  In-Memory DB (go-memdb)     │
                                │    ├─ id (primary)           │
                                │    ├─ user_id index          │
                                │    ├─ group_id index         │
                                │    ├─ channel_id index       │
                                │    └─ channel_group compound │
                                │                              │
                                │  MySQL (oturum doğrulama)    │
                                └──────────────────────────────┘
```

**Temel prensipler:**
- **In-memory bağlantı yönetimi** — `go-memdb` ile O(1) lookup, indeksli sorgulama
- **Mutex korumalı yazma** — Her bağlantıda `sync.Mutex` ile concurrent write güvenliği
- **Ping/Pong keep-alive** — 30 saniyelik aralıklarla bağlantı canlılığı kontrolü
- **Otomatik temizlik** — Bağlantı kapandığında `defer` ile memdb'den otomatik silinme

---

## 🛠 Teknoloji Yığını

| Bileşen | Teknoloji | Sürüm |
|---------|-----------|-------|
| Dil | Go | 1.21.10 |
| WebSocket | gorilla/websocket | 1.5.3 |
| In-Memory DB | hashicorp/go-memdb | 1.3.5 |
| Veritabanı | MySQL (go-sql-driver) | 1.9.3 |
| Test | Node.js + ws | 8.18.0 |

---

## 🚀 Kurulum

### Gereksinimler
- Go 1.21+
- MySQL 5.7+ (production modu için)
- Node.js 18+ (stres testi için)

### Derleme

```bash
# Bağımlılıkları indir
go mod download

# Derle
go build -o api.topluyo.com.exe .
```

---

## 💻 Kullanım

### Sunucuyu Başlatma

```bash
# Production mod
./api.topluyo.com.exe port=8085

# Test modu (auth bypass, MySQL gerektirmez)
./api.topluyo.com.exe port=8085 env=test
```

### Komut Satırı Argümanları

| Argüman | Açıklama | Örnek |
|---------|----------|-------|
| `port` | Sunucunun dinleyeceği port | `port=8085` |
| `env` | Çalışma ortamı (`test` = auth bypass) | `env=test` |

---

## 🔌 WebSocket Protokolü

### Endpoint
```
ws://host:port/!direct
```

### Bağlantı Akışı

```
Kullanıcı                          Sunucu
   │                                  │
   │──── WebSocket Bağlantısı ───────►│
   │                                  │
   │──── 1. Mesaj: Auth Token ───────►│  Token ile DB'den user_id alınır
   │                                  │  Token yoksa IP hash ile negatif ID üretilir
   │                                  │
   │──── "grupID,kanalID" ──────────►│  Kullanıcıyı belirtilen gruba/kanala atar
   │                                  │
   │──── ":mesaj içeriği" ──────────►│  ":" ile başlayan mesajlar broadcast edilir
   │                                  │  (aynı grup+kanal'daki herkese)
   │                                  │
   │◄─── Broadcast mesajlar ─────────│
   │                                  │
   │◄──── Ping (her 30s) ───────────│  Keep-alive
   │──── Pong ──────────────────────►│
   │                                  │
```

### Mesaj Türleri

| Mesaj Formatı | Yön | Açıklama |
|---------------|-----|----------|
| `<token>` | İstemci → Sunucu | İlk mesaj: auth token (boş da olabilir) |
| `<grupID>,<kanalID>` | İstemci → Sunucu | Grup ve kanal değiştirme |
| `:<mesaj>` | İstemci → Sunucu | Broadcast mesaj gönderme (`:`  önceki kısım atılır) |
| `<herhangi>` | Sunucu → İstemci | Broadcast ile iletilen mesajlar |

### Bağlantı Zaman Aşımları
- **Read Deadline:** 60 saniye (pong ile yenilenir)
- **Ping Interval:** 30 saniye
- **Read Limit:** 512 byte

---

## 📡 HTTP API

### `GET /!direct-signal`

Sunucu tarafından (backend → .direct) hedefe mesaj göndermek için kullanılır. Bu endpoint, ana uygulamanın WebSocket sunucusuna dışarıdan sinyal göndermesini sağlar.

**Query Parametreleri:**

| Parametre | Format | Açıklama |
|-----------|--------|----------|
| `part` | `grupID,kanalID,userID` | Hedef filtresi (0 = filtre yok) |
| `message` | `string` | Gönderilecek mesaj içeriği |

**Örnekler:**

```bash
# Belirli bir gruba mesaj gönder
curl "http://localhost:8085/!direct-signal?part=5,0,0&message=merhaba"

# Belirli bir kanal + gruba mesaj gönder
curl "http://localhost:8085/!direct-signal?part=5,3,0&message=kanal-mesaj"

# Belirli bir kullanıcıya mesaj gönder
curl "http://localhost:8085/!direct-signal?part=0,0,42&message=özel-mesaj"

# Herkese broadcast
curl "http://localhost:8085/!direct-signal?part=0,0,0&message=genel-duyuru"
```

### Broadcast Filtreleme Mantığı

Filtreler `part` parametresindeki `grupID`, `kanalID` ve `userID` değerlerine göre çalışır. `0` değeri "filtre yok" anlamına gelir:

| grupID | kanalID | userID | Hedef |
|--------|---------|--------|-------|
| 0 | 0 | 0 | Tüm bağlantılar |
| 5 | 0 | 0 | Grup 5'teki herkes |
| 0 | 3 | 0 | Kanal 3'teki herkes |
| 5 | 3 | 0 | Grup 5 + Kanal 3 (compound index) |
| 0 | 0 | 42 | Yalnızca user_id=42 |

> **Not:** Compound index (`channel_group`) sadece hem `kanalID` hem `grupID` belirtildiğinde kullanılır ve en verimli filtreleme yöntemidir.

---

## 🗄 Veritabanı

### MySQL Bağlantısı

```
Kullanıcı: master
Şifre:     master
Host:      127.0.0.1:3306
Veritabanı: db
```

### Kullanılan Tablolar

#### `session` — Oturum Doğrulama
```sql
SELECT user_id FROM session WHERE token = ? AND expire > UNIX_TIMESTAMP()
```

#### `user` — Kullanıcı Bilgileri
```sql
SELECT id, name, nick, image FROM user WHERE id = ? AND blocked = 0
```

### Kullanıcı Kimlik Tespiti

1. **Token ile:** İstemcinin gönderdiği token, `session` tablosunda aranır
2. **IP Hash ile:** Token yoksa veya geçersizse, kullanıcının IP adresi FNV-32a ile hash'lenir ve negatif tam sayıya dönüştürülür
   - IP çözümleme sırası: `CF-Connecting-IP` → `X-Forwarded-For` → `X-Real-IP` → `RemoteAddr`
   - Negatif ID'ler kayıtsız (anonim) kullanıcıları temsil eder

---

## 🧪 Test Modu

Sunucu `env=test` argümanı ile başlatıldığında:

- ✅ **Auth bypass:** Token doğrulama atlanır, her bağlantıya auto-increment ID verilir
- ✅ **MySQL opsiyonel:** Veritabanı bağlantısı başarısız olursa sunucu çökmez
- ✅ **Atomic counter:** `sync/atomic` ile thread-safe kullanıcı ID üretimi

```bash
go run main.go port=8085 env=test
```

---

## 📊 Stres Testi

`test/` dizininde Node.js tabanlı kapsamlı bir stres test aracı bulunur.

### Kurulum

```bash
cd test
npm install
```

### Çalıştırma

```bash
# Varsayılan: 500 bağlantı, 50'lik batch, 100ms aralık, 10s süre
npm test

# 1.000 bağlantı testi
npm run test:1k

# 5.000 bağlantı testi
npm run test:5k

# 10.000 bağlantı testi
npm run test:10k

# Özelleştirilmiş parametreler
node stress.js --url=ws://localhost:8085/!direct --connections=2000 --batch=100 --delay=50 --duration=20
```

### Parametreler

| Parametre | Varsayılan | Açıklama |
|-----------|-----------|----------|
| `--url` | `ws://localhost:8085/!direct` | WebSocket sunucu adresi |
| `--connections` | `500` | Toplam bağlantı sayısı |
| `--batch` | `50` | Her batch'teki bağlantı sayısı |
| `--delay` | `100` | Batch arası bekleme süresi (ms) |
| `--duration` | `10` | Bağlantıları açık tutma süresi (saniye) |

### Test Senaryosu

Her bağlantı şu adımları izler:
1. WebSocket bağlantısı açılır
2. Auth token gönderilir (`test-token-N`)
3. Grup ve kanala katılır (`grupID,kanalID`)
4. Her 10. bağlantı bir broadcast mesaj gönderir (`:hello from #N`)
5. Belirtilen süre boyunca açık tutulur
6. Tüm bağlantılar kapatılır ve detaylı rapor yazdırılır

### Rapor Metrikleri

- Başarılı / başarısız bağlantı sayısı ve oranı
- Ortalama, minimum, maksimum ve P95 bağlantı süresi
- Gönderilen ve alınan mesaj sayısı
- Hata özeti (gruplandırılmış)

---

## 📁 Proje Yapısı

```
.direct/
├── main.go                 # Ana sunucu kodu (WebSocket + HTTP + Auth)
├── go.mod                  # Go modül tanımı
├── go.sum                  # Bağımlılık hash'leri
├── api.topluyo.com.exe     # Derlenmiş binary
├── LICENSE                 # MIT Lisansı
├── README.md               # Bu dosya
└── test/
    ├── stress.js           # WebSocket stres test aracı
    ├── package.json        # Node.js bağımlılıkları ve scriptleri
    └── package-lock.json   # Kilitlenmiş bağımlılık sürümleri
```

### `main.go` Kod Organizasyonu

| Bölüm | Satırlar | Açıklama |
|--------|----------|----------|
| **Connection struct** | 22–38 | Bağlantı veri yapısı ve thread-safe yazma |
| **MemDB Schema** | 50–95 | İndeks tanımları (id, user_id, group_id, channel_id, compound) |
| **Connection CRUD** | 97–134 | `setConn`, `delConn`, `getConn` fonksiyonları |
| **Broadcast** | 136–181 | Hedefli mesaj gönderim motoru |
| **Users Query** | 183–222 | Filtrelenmiş bağlantı listesi sorgulama |
| **Helpers** | 224–244 | `ToInt`, `ToString`, `ToJson` dönüştürücüler |
| **Main & HTTP** | 246–286 | Sunucu başlatma, HTTP route tanımları |
| **WebSocket Handler** | 288–354 | Bağlantı yaşam döngüsü (auth → subscribe → broadcast) |
| **User Identification** | 356–414 | Token doğrulama, IP hash, kullanıcı bilgisi sorgulama |
| **Argument Parser** | 416–426 | Komut satırı argüman ayrıştırıcı |

---

## 📄 Lisans

MIT License — Detaylar için [LICENSE](LICENSE) dosyasına bakın.

© 2026 Topluyo
