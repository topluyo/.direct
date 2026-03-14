/**
 * WebSocket Kapasite & Stres Testi
 * 
 * Kullanım:
 *   node stress.js [--url=ws://host:port/path] [--connections=N] [--batch=N] [--delay=ms] [--duration=s]
 * 
 * Önce Go sunucusunu test modunda başlatın:
 *   go run main.go port=8085 env=test
 */

const WebSocket = require('ws');

// ── Argüman parse ────────────────────────────────────────────────
function parseArgs() {
  const defaults = {
    url: 'ws://localhost:8085/!direct',
    connections: 500,
    batch: 50,
    delay: 100,       // batch arası bekleme (ms)
    duration: 10,     // bağlantıları açık tutma süresi (saniye)
  };

  for (const arg of process.argv.slice(2)) {
    const [key, val] = arg.replace(/^--/, '').split('=');
    if (key in defaults) {
      defaults[key] = isNaN(Number(val)) ? val : Number(val);
    }
  }
  return defaults;
}

const config = parseArgs();

// ── Metrikler ────────────────────────────────────────────────────
const metrics = {
  attempted: 0,
  connected: 0,
  failed: 0,
  messagesReceived: 0,
  messagesSent: 0,
  errors: [],
  connectTimes: [],     // ms
  startTime: 0,
  endTime: 0,
};

const sockets = [];

// ── Tek bağlantı aç ─────────────────────────────────────────────
function openConnection(index) {
  return new Promise((resolve) => {
    metrics.attempted++;
    const t0 = Date.now();

    const ws = new WebSocket(config.url);
    let resolved = false;

    const timeout = setTimeout(() => {
      if (!resolved) {
        resolved = true;
        metrics.failed++;
        metrics.errors.push(`#${index} timeout`);
        try { ws.terminate(); } catch {}
        resolve(null);
      }
    }, 10000);

    ws.on('open', () => {
      clearTimeout(timeout);
      if (resolved) return;
      resolved = true;

      const connectTime = Date.now() - t0;
      metrics.connected++;
      metrics.connectTimes.push(connectTime);
      sockets.push(ws);

      // Auth mesajı gönder (test modunda herhangi bir token yeter)
      ws.send(`test-token-${index}`);
      metrics.messagesSent++;

      // Grup/kanal'a katıl
      const groupId = (index % 10) + 1;
      const channelId = (index % 5) + 1;
      setTimeout(() => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(`${groupId},${channelId}`);
          metrics.messagesSent++;
        }
      }, 50);

      // Broadcast mesaj gönder (her 10. bağlantı)
      if (index % 10 === 0) {
        setTimeout(() => {
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(`:hello from #${index}`);
            metrics.messagesSent++;
          }
        }, 200);
      }

      resolve(ws);
    });

    ws.on('message', () => {
      metrics.messagesReceived++;
    });

    ws.on('error', (err) => {
      clearTimeout(timeout);
      if (!resolved) {
        resolved = true;
        metrics.failed++;
        metrics.errors.push(`#${index} ${err.code || err.message}`);
        resolve(null);
      }
    });

    ws.on('close', () => {
      clearTimeout(timeout);
      if (!resolved) {
        resolved = true;
        metrics.failed++;
        resolve(null);
      }
    });
  });
}

// ── Batch halinde bağlantı aç ────────────────────────────────────
async function openBatch(startIndex, count) {
  const promises = [];
  for (let i = 0; i < count; i++) {
    promises.push(openConnection(startIndex + i));
  }
  return Promise.all(promises);
}

// ── Tüm bağlantıları kapat ──────────────────────────────────────
function closeAll() {
  let closed = 0;
  for (const ws of sockets) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.close();
      closed++;
    }
  }
  return closed;
}

// ── Rapor yazdır ─────────────────────────────────────────────────
function printReport() {
  const totalTime = metrics.endTime - metrics.startTime;
  const avgConnect = metrics.connectTimes.length > 0
    ? (metrics.connectTimes.reduce((a, b) => a + b, 0) / metrics.connectTimes.length).toFixed(1)
    : 'N/A';
  const maxConnect = metrics.connectTimes.length > 0
    ? Math.max(...metrics.connectTimes)
    : 'N/A';
  const minConnect = metrics.connectTimes.length > 0
    ? Math.min(...metrics.connectTimes)
    : 'N/A';
  const p95Index = Math.floor(metrics.connectTimes.length * 0.95);
  const sorted = [...metrics.connectTimes].sort((a, b) => a - b);
  const p95 = sorted.length > 0 ? sorted[p95Index] : 'N/A';

  console.log('\n' + '═'.repeat(60));
  console.log('  📊 SOCKET KAPASİTE TESTİ RAPORU');
  console.log('═'.repeat(60));
  console.log(`  🔗 URL              : ${config.url}`);
  console.log(`  📦 Batch büyüklüğü  : ${config.batch}`);
  console.log(`  ⏱  Toplam süre      : ${(totalTime / 1000).toFixed(1)}s`);
  console.log('─'.repeat(60));
  console.log(`  ✅ Başarılı bağlantı : ${metrics.connected} / ${metrics.attempted}`);
  console.log(`  ❌ Başarısız         : ${metrics.failed}`);
  console.log(`  📈 Başarı oranı     : ${((metrics.connected / metrics.attempted) * 100).toFixed(1)}%`);
  console.log('─'.repeat(60));
  console.log(`  ⏱  Ort bağlantı süresi : ${avgConnect} ms`);
  console.log(`  ⏱  Min bağlantı süresi : ${minConnect} ms`);
  console.log(`  ⏱  Max bağlantı süresi : ${maxConnect} ms`);
  console.log(`  ⏱  P95 bağlantı süresi : ${p95} ms`);
  console.log('─'.repeat(60));
  console.log(`  📤 Gönderilen mesaj  : ${metrics.messagesSent}`);
  console.log(`  📥 Alınan mesaj      : ${metrics.messagesReceived}`);
  console.log('═'.repeat(60));

  if (metrics.errors.length > 0) {
    const uniqueErrors = {};
    for (const e of metrics.errors) {
      const key = e.replace(/#\d+/, '#N');
      uniqueErrors[key] = (uniqueErrors[key] || 0) + 1;
    }
    console.log('\n  ⚠  Hata özeti:');
    for (const [err, count] of Object.entries(uniqueErrors)) {
      console.log(`     ${err} (x${count})`);
    }
  }

  console.log('');
}

// ── Ana akış ─────────────────────────────────────────────────────
async function main() {
  console.log('═'.repeat(60));
  console.log('  🚀 WebSocket Kapasite Testi Başlıyor');
  console.log(`  Hedef: ${config.connections} bağlantı — ${config.batch} batch — ${config.delay}ms aralık`);
  console.log('═'.repeat(60));

  metrics.startTime = Date.now();

  let opened = 0;
  while (opened < config.connections) {
    const batchSize = Math.min(config.batch, config.connections - opened);
    process.stdout.write(`\r  ⏳ Bağlantı açılıyor... ${opened + batchSize}/${config.connections}`);
    await openBatch(opened, batchSize);
    opened += batchSize;

    if (opened < config.connections) {
      await new Promise(r => setTimeout(r, config.delay));
    }
  }

  console.log(`\n  ✅ Tüm bağlantılar açıldı. ${config.duration}s boyunca açık tutulacak...`);

  // Bağlantıları açık tut
  await new Promise(r => setTimeout(r, config.duration * 1000));

  metrics.endTime = Date.now();

  console.log('  🔄 Bağlantılar kapatılıyor...');
  const closed = closeAll();
  console.log(`  ✅ ${closed} bağlantı kapatıldı.`);

  // Kapanmaları bekle
  await new Promise(r => setTimeout(r, 1000));

  printReport();
  process.exit(0);
}

main().catch(err => {
  console.error('Fatal error:', err);
  process.exit(1);
});
