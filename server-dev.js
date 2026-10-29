// server-dev.js
// Local Test Server untuk Oper menggunakan native Node.js (v18+)
// Dibuat agar Anda bisa langsung menguji aplikasi di browser tanpa perlu install Go terlebih dahulu.
// Jalankan dengan: node server-dev.js

const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const os = require('node:os');

const PORT = process.env.PORT || 8080;
const DATA_DIR = path.join(os.tmpdir(), 'oper-dev');
if (!fs.existsSync(DATA_DIR)) fs.mkdirSync(DATA_DIR, { recursive: true });

const rooms = new Map();
const pins = new Map();

function generatePin() {
  for (let i = 0; i < 100; i++) {
    const pin = String(Math.floor(Math.random() * 10000)).padStart(4, '0');
    if (!pins.has(pin)) return pin;
  }
  return String(Math.floor(Math.random() * 10000)).padStart(4, '0');
}

function broadcast(room, type, data) {
  const payload = `event: ${type}\ndata: ${JSON.stringify(data)}\n\n`;
  for (const res of room.subscribers) {
    try {
      res.write(payload);
    } catch {
      room.subscribers.delete(res);
    }
  }
}

function sendJson(res, status, data) {
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
    'Access-Control-Allow-Origin': '*',
  });
  res.end(JSON.stringify(data));
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
  const pathname = url.pathname;

  // CORS preflight
  if (req.method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, POST, DELETE, OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization',
    });
    return res.end();
  }

  // 1. Static UI Frontend (index.html, manifest, sw, icons)
  if (req.method === 'GET' && (pathname === '/' || pathname === '/index.html')) {
    const indexPath = path.join(__dirname, 'web', 'index.html');
    fs.readFile(indexPath, 'utf8', (err, html) => {
      if (err) {
        res.writeHead(500);
        return res.end('Error loading index.html');
      }
      res.writeHead(200, {
        'Content-Type': 'text/html; charset=utf-8',
        'Cache-Control': 'no-cache',
      });
      res.end(html);
    });
    return;
  }

  if (req.method === 'GET' && ['/manifest.json', '/sw.js', '/icon-192.png', '/icon-512.png'].includes(pathname)) {
    const file = path.join(__dirname, 'web', pathname.slice(1));
    const mime = pathname.endsWith('.json') ? 'application/manifest+json' : pathname.endsWith('.js') ? 'application/javascript' : 'image/png';
    fs.readFile(file, (err, data) => {
      if (err) { res.writeHead(404); return res.end(); }
      res.writeHead(200, { 'Content-Type': mime, 'Cache-Control': 'no-cache' });
      res.end(data);
    });
    return;
  }

  // 2. Health check
  if (req.method === 'GET' && pathname === '/healthz') {
    return sendJson(res, 200, {
      ok: true,
      mode: 'node-dev-server',
      rooms: rooms.size,
      memory: process.memoryUsage(),
    });
  }

  // 3. Create Room (POST /api/rooms)
  if (req.method === 'POST' && pathname === '/api/rooms') {
    const roomId = crypto.randomBytes(16).toString('hex');
    const pin = generatePin();
    const joinKey = crypto.randomBytes(16).toString('hex');
    const receiverToken = crypto.randomBytes(24).toString('hex');
    const senderToken = crypto.randomBytes(24).toString('hex');
    const now = new Date();
    const expiresAt = new Date(now.getTime() + 15 * 60 * 1000);

    const room = {
      id: roomId,
      pin,
      joinKey,
      receiverToken,
      senderToken,
      expiresAt,
      createdAt: now,
      files: new Map(),
      senders: 0,
      subscribers: new Set(),
      receiverSubs: new Set(),
      senderSubs: new Set(),
      offlineTimer: null,
      dir: path.join(DATA_DIR, roomId),
    };
    fs.mkdirSync(room.dir, { recursive: true });

    rooms.set(roomId, room);
    pins.set(pin, room);

    return sendJson(res, 201, {
      roomId,
      pin,
      joinKey,
      receiverToken,
      expiresAt: expiresAt.toISOString(),
      now: now.toISOString(),
      ttlSeconds: 900,
      maxFileBytes: 50 * 1024 * 1024,
      senders: 0,
    });
  }

  // 4. Join Room (POST /api/join)
  if (req.method === 'POST' && pathname === '/api/join') {
    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', () => {
      try {
        const { pin, roomId, joinKey } = JSON.parse(body || '{}');
        let room = null;
        if (roomId && joinKey) {
          const r = rooms.get(roomId);
          if (r && r.joinKey === joinKey) room = r;
        } else if (pin) {
          room = pins.get(pin);
        }

        if (!room || new Date() > room.expiresAt) {
          return sendJson(res, 404, { error: 'PIN salah atau ruangan sudah kedaluwarsa' });
        }

        // Rotasi PIN & joinKey sekali pakai
        pins.delete(room.pin);
        room.pin = generatePin();
        pins.set(room.pin, room);
        room.joinKey = crypto.randomBytes(16).toString('hex');
        if (room.senders < room.senderSubs.size + 1) {
          room.senders = room.senderSubs.size + 1;
        }

        // Broadcast event joined ke receiver PC
        broadcast(room, 'joined', {
          roomId: room.id,
          pin: room.pin,
          joinKey: room.joinKey,
          expiresAt: room.expiresAt.toISOString(),
          ttlSeconds: 900,
          maxFileBytes: 50 * 1024 * 1024,
          senders: room.senders,
        });

        return sendJson(res, 200, {
          roomId: room.id,
          senderToken: room.senderToken,
          expiresAt: room.expiresAt.toISOString(),
          maxFileBytes: 50 * 1024 * 1024,
        });
      } catch {
        return sendJson(res, 400, { error: 'Invalid JSON request' });
      }
    });
    return;
  }

  // 5. SSE Events (GET /api/rooms/:id/events?t=)
  const eventsMatch = pathname.match(/^\/api\/rooms\/([a-f0-9]{32})\/events$/);
  if (req.method === 'GET' && eventsMatch) {
    const roomId = eventsMatch[1];
    const token = url.searchParams.get('t');
    const room = rooms.get(roomId);

    const isReceiver = room && room.receiverToken === token;
    const isSender = room && room.senderToken === token;

    if (!room || (!isReceiver && !isSender)) {
      return sendJson(res, 403, { error: 'Akses ditolak' });
    }

    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache, no-transform',
      'Connection': 'keep-alive',
      'X-Accel-Buffering': 'no',
    });

    room.subscribers.add(res);

    if (isReceiver) {
      room.receiverSubs.add(res);
      if (room.offlineTimer) {
        clearTimeout(room.offlineTimer);
        room.offlineTimer = null;
        console.log(`[Room ${roomId.slice(0, 8)}] Receiver reconnected, offline grace timer cancelled.`);
      }
    } else if (isSender) {
      room.senderSubs.add(res);
      room.senders = room.senderSubs.size;
      broadcast(room, 'room', {
        roomId: room.id,
        pin: room.pin,
        joinKey: room.joinKey,
        expiresAt: room.expiresAt.toISOString(),
        ttlSeconds: 900,
        maxFileBytes: 50 * 1024 * 1024,
        senders: room.senders,
      });
    }

    // Kirim snapshot awal sesuai hak akses
    if (isReceiver) {
      const snapshot = {
        room: {
          roomId: room.id,
          pin: room.pin,
          joinKey: room.joinKey,
          expiresAt: room.expiresAt.toISOString(),
          now: new Date().toISOString(),
          ttlSeconds: 900,
          maxFileBytes: 50 * 1024 * 1024,
          senders: room.senders,
        },
        files: Array.from(room.files.values()),
      };
      res.write(`event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`);
    } else {
      // Sender: kirim status active saja (tanpa membocorkan PIN/joinKey/file list)
      res.write(`event: snapshot\ndata: ${JSON.stringify({ role: 'sender', active: true, roomId: room.id })}\n\n`);
    }

    const timer = setInterval(() => {
      try { res.write(': ping\n\n'); } catch { clearInterval(timer); }
    }, 25000);

    req.on('close', () => {
      clearInterval(timer);
      room.subscribers.delete(res);

      if (isSender) {
        room.senderSubs.delete(res);
        room.senders = room.senderSubs.size;
        broadcast(room, 'room', {
          roomId: room.id,
          pin: room.pin,
          joinKey: room.joinKey,
          expiresAt: room.expiresAt.toISOString(),
          ttlSeconds: 900,
          maxFileBytes: 50 * 1024 * 1024,
          senders: room.senders,
        });
      } else if (isReceiver) {
        room.receiverSubs.delete(res);
        if (room.receiverSubs.size === 0) {
          if (room.offlineTimer) clearTimeout(room.offlineTimer);
          room.offlineTimer = setTimeout(() => {
            const targetRoom = rooms.get(roomId);
            if (targetRoom && targetRoom.receiverSubs.size === 0) {
              console.log(`[Room ${roomId.slice(0, 8)}] Receiver offline grace period expired, closing room.`);
              broadcast(targetRoom, 'expired', { reason: 'closed' });
              for (const sub of targetRoom.subscribers) sub.end();
              pins.delete(targetRoom.pin);
              rooms.delete(roomId);
              if (fs.existsSync(targetRoom.dir)) fs.rmSync(targetRoom.dir, { recursive: true, force: true });
            }
          }, 10000);
        }
      }
    });
    return;
  }

  // 6. Upload File (POST /api/rooms/:id/files)
  const uploadMatch = pathname.match(/^\/api\/rooms\/([a-f0-9]{32})\/files$/);
  if (req.method === 'POST' && uploadMatch) {
    const roomId = uploadMatch[1];
    const authHeader = req.headers['authorization'] || '';
    const token = authHeader.replace(/^Bearer\s+/i, '').trim();
    const room = rooms.get(roomId);

    if (!room || room.senderToken !== token) {
      return sendJson(res, 403, { error: 'Akses ditolak (senderToken tidak valid)' });
    }

    const contentType = req.headers['content-type'] || '';
    const boundaryMatch = contentType.match(/boundary=(?:"([^"]+)"|([^;]+))/i);
    if (!boundaryMatch) {
      return sendJson(res, 400, { error: 'Bukan form multipart yang valid' });
    }
    const boundary = boundaryMatch[1] || boundaryMatch[2];

    const chunks = [];
    req.on('data', chunk => chunks.push(chunk));
    req.on('end', () => {
      const buffer = Buffer.concat(chunks);
      const boundaryBuf = Buffer.from(`--${boundary}`);
      const parts = [];
      let start = 0;

      while ((start = buffer.indexOf(boundaryBuf, start)) !== -1) {
        start += boundaryBuf.length;
        const end = buffer.indexOf(boundaryBuf, start);
        if (end !== -1) {
          parts.push(buffer.subarray(start, end));
        }
      }

      let fileData = null;
      let filename = 'file.bin';

      for (const part of parts) {
        const headerEnd = part.indexOf('\r\n\r\n');
        if (headerEnd === -1) continue;
        const headers = part.subarray(0, headerEnd).toString('utf8');
        const fnMatch = headers.match(/filename="([^"]+)"/i);
        if (fnMatch) {
          filename = fnMatch[1];
          // hilangkan CRLF di akhir part data
          let dataEnd = part.length;
          if (part[dataEnd - 2] === 13 && part[dataEnd - 1] === 10) dataEnd -= 2;
          fileData = part.subarray(headerEnd + 4, dataEnd);
          break;
        }
      }

      if (!fileData) {
        return sendJson(res, 400, { error: 'File tidak ditemukan dalam request' });
      }

      // Deteksi tipe file berdasarkan Magic Bytes (Header) dan ekstensi
      function inspectFile(buf, origName) {
        let name = (origName || 'file').trim();
        // Bersihkan path traversal
        name = name.replace(/\\/g, '/');
        const lastSlash = name.lastIndexOf('/');
        if (lastSlash >= 0) name = name.slice(lastSlash + 1);

        let ext = path.extname(name).toLowerCase();
        let mime = 'application/octet-stream';
        let isInline = false;

        // Sniffing Magic Bytes
        if (buf.length >= 4) {
          // PDF: %PDF
          if (buf[0] === 0x25 && buf[1] === 0x50 && buf[2] === 0x44 && buf[3] === 0x46) {
            mime = 'application/pdf';
            isInline = true;
            if (!ext) ext = '.pdf';
          }
          // PNG: \x89PNG
          else if (buf[0] === 0x89 && buf[1] === 0x50 && buf[2] === 0x4E && buf[3] === 0x47) {
            mime = 'image/png';
            isInline = true;
            if (!ext) ext = '.png';
          }
          // JPEG: \xFF\xD8\xFF
          else if (buf[0] === 0xFF && buf[1] === 0xD8 && buf[2] === 0xFF) {
            mime = 'image/jpeg';
            isInline = true;
            if (!ext) ext = '.jpg';
          }
          // GIF: GIF8
          else if (buf[0] === 0x47 && buf[1] === 0x49 && buf[2] === 0x46 && buf[3] === 0x38) {
            mime = 'image/gif';
            isInline = true;
            if (!ext) ext = '.gif';
          }
          // WEBP: RIFF....WEBP
          else if (buf.length >= 12 && buf.subarray(0, 4).toString() === 'RIFF' && buf.subarray(8, 12).toString() === 'WEBP') {
            mime = 'image/webp';
            isInline = true;
            if (!ext) ext = '.webp';
          }
          // Office OpenXML / ZIP: PK\x03\x04
          else if (buf[0] === 0x50 && buf[1] === 0x4B && buf[2] === 0x03 && buf[3] === 0x04) {
            const headStr = buf.subarray(0, Math.min(buf.length, 4096)).toString('binary');
            if (headStr.includes('xl/')) {
              mime = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet';
              if (!ext) ext = '.xlsx';
            } else if (headStr.includes('word/')) {
              mime = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document';
              if (!ext) ext = '.docx';
            } else if (headStr.includes('ppt/')) {
              mime = 'application/vnd.openxmlformats-officedocument.presentationml.presentation';
              if (!ext) ext = '.pptx';
            } else {
              mime = 'application/zip';
              if (!ext) ext = '.zip';
            }
          }
          // Legacy MS Office (DOC/XLS): \xD0\xCF\x11\xE0
          else if (buf[0] === 0xD0 && buf[1] === 0xCF && buf[2] === 0x11 && buf[3] === 0xE0) {
            mime = 'application/msword';
            if (!ext) ext = '.doc';
          }
        }

        // Cek jika teks biasa (ASCII/UTF-8)
        if (mime === 'application/octet-stream' && ext === '.txt') {
          mime = 'text/plain; charset=utf-8';
          isInline = true;
        }

        // Jika nama file tidak memiliki ekstensi atau berupa UUID murni tanpa ekstensi,
        // tambahkan ekstensi yang cocok agar dikenali Windows / OS
        if (ext && !name.toLowerCase().endsWith(ext)) {
          name = name + ext;
        }

        return { mime, isInline, safeName: name };
      }

      const { mime, isInline, safeName } = inspectFile(fileData, filename);

      const fileId = crypto.randomBytes(12).toString('hex');
      const filePath = path.join(room.dir, fileId);
      fs.writeFileSync(filePath, fileData);

      const fileMeta = {
        id: fileId,
        name: safeName,
        size: fileData.length,
        mime,
        inline: isInline,
        createdAt: new Date().toISOString(),
        expiresAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      };

      room.files.set(fileId, fileMeta);

      // Broadcast file event ke penerima
      broadcast(room, 'file', fileMeta);

      return sendJson(res, 201, fileMeta);
    });
    return;
  }

  // 6b. Send Text / Link (POST /api/rooms/:id/text)
  const textMatch = pathname.match(/^\/api\/rooms\/([a-f0-9]{32})\/text$/);
  if (req.method === 'POST' && textMatch) {
    const roomId = textMatch[1];
    const authHeader = req.headers['authorization'] || '';
    const token = authHeader.replace(/^Bearer\s+/i, '').trim();
    const room = rooms.get(roomId);

    if (!room || room.senderToken !== token) {
      return sendJson(res, 403, { error: 'Akses ditolak (senderToken tidak valid)' });
    }

    let body = '';
    req.on('data', chunk => { body += chunk; });
    req.on('end', () => {
      let json = {};
      try { json = JSON.parse(body); } catch {}
      const text = (json.text || '').trim();
      if (!text) {
        return sendJson(res, 400, { error: 'Teks tidak boleh kosong' });
      }

      const fileId = crypto.randomBytes(12).toString('hex');
      const filePath = path.join(room.dir, fileId);
      fs.writeFileSync(filePath, text, 'utf8');

      let name = 'Catatan Teks';
      const isUrl = /^https?:\/\/[^\s]+$/.test(text);
      if (isUrl) {
        name = text.length > 60 ? 'Tautan: ' + text.slice(0, 57) + '…' : 'Tautan: ' + text;
      } else {
        const firstLine = text.split('\n')[0].trim();
        if (firstLine) name = firstLine.length > 50 ? firstLine.slice(0, 47) + '…' : firstLine;
      }

      const fileMeta = {
        id: fileId,
        name,
        size: Buffer.byteLength(text, 'utf8'),
        mime: 'text/plain; charset=utf-8',
        inline: true,
        text,
        createdAt: new Date().toISOString(),
        expiresAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      };

      room.files.set(fileId, fileMeta);
      broadcast(room, 'file', fileMeta);
      return sendJson(res, 201, fileMeta);
    });
    return;
  }

  // 7. Download / Preview File (GET /api/rooms/:id/files/:fid?t=&dl=)
  const fileMatch = pathname.match(/^\/api\/rooms\/([a-f0-9]{32})\/files\/([a-f0-9]{24})$/);
  if (req.method === 'GET' && fileMatch) {
    const roomId = fileMatch[1];
    const fileId = fileMatch[2];
    const token = url.searchParams.get('t');
    const dl = url.searchParams.get('dl') === '1';
    const room = rooms.get(roomId);

    if (!room || room.receiverToken !== token) {
      return sendJson(res, 403, { error: 'Akses ditolak' });
    }

    const fileMeta = room.files.get(fileId);
    const filePath = path.join(room.dir, fileId);

    if (!fileMeta || !fs.existsSync(filePath)) {
      return sendJson(res, 404, { error: 'File tidak ditemukan' });
    }

    const stat = fs.statSync(filePath);
    const disposition = (dl || !fileMeta.inline) ? 'attachment' : 'inline';

    let dlName = fileMeta.name;
    if (fileMeta.text) {
      if (/^https?:\/\//i.test(fileMeta.text.trim())) {
        dlName = 'tautan.txt';
      } else {
        dlName = dlName.replace(/[\\/:*?"<>|]/g, '_').trim() || 'catatan';
        if (!dlName.toLowerCase().endsWith('.txt')) {
          dlName += '.txt';
        }
      }
    }

    // Format RFC 6266 Content-Disposition:
    // filename="ascii_safe" dan filename*=UTF-8''encoded
    const asciiSafe = dlName.replace(/[^\x20-\x7E]/g, '_').replace(/"/g, '\\"');
    const utf8Encoded = encodeURIComponent(dlName);

    res.writeHead(200, {
      'Content-Type': fileMeta.mime,
      'Content-Length': stat.size,
      'Content-Disposition': `${disposition}; filename="${asciiSafe}"; filename*=UTF-8''${utf8Encoded}`,
      'Cache-Control': 'private, no-store',
    });

    const stream = fs.createReadStream(filePath);
    stream.pipe(res);
    return;
  }

  // 8. Delete File (DELETE /api/rooms/:id/files/:fid)
  if (req.method === 'DELETE' && fileMatch) {
    const roomId = fileMatch[1];
    const fileId = fileMatch[2];
    const authHeader = req.headers['authorization'] || '';
    const token = authHeader.replace(/^Bearer\s+/i, '').trim();
    const room = rooms.get(roomId);

    if (!room || room.receiverToken !== token) {
      return sendJson(res, 403, { error: 'Akses ditolak' });
    }

    room.files.delete(fileId);
    const filePath = path.join(room.dir, fileId);
    if (fs.existsSync(filePath)) fs.unlinkSync(filePath);

    broadcast(room, 'file_deleted', { id: fileId });
    res.writeHead(204);
    return res.end();
  }

  // 9. Close Room (DELETE /api/rooms/:id)
  const roomDelMatch = pathname.match(/^\/api\/rooms\/([a-f0-9]{32})$/);
  if (req.method === 'DELETE' && roomDelMatch) {
    const roomId = roomDelMatch[1];
    const authHeader = req.headers['authorization'] || '';
    const token = authHeader.replace(/^Bearer\s+/i, '').trim();
    const room = rooms.get(roomId);

    if (!room || room.receiverToken !== token) {
      return sendJson(res, 403, { error: 'Akses ditolak' });
    }

    if (room.offlineTimer) clearTimeout(room.offlineTimer);
    broadcast(room, 'expired', { reason: 'closed' });
    for (const sub of room.subscribers) sub.end();
    pins.delete(room.pin);
    rooms.delete(roomId);
    if (fs.existsSync(room.dir)) fs.rmSync(room.dir, { recursive: true, force: true });

    res.writeHead(204);
    return res.end();
  }

  res.writeHead(404, { 'Content-Type': 'text/plain' });
  res.end('Not Found');
});

server.listen(PORT, '0.0.0.0', () => {
  console.log(`\n======================================================`);
  console.log(`⚡ Oper Local Test Server AKTIF`);
  console.log(`======================================================`);
  console.log(`- URL Lokal PC  : http://localhost:${PORT}`);
  console.log(`- UI Web        : http://127.0.0.1:${PORT}`);
  console.log(`- Status        : Siap dites di browser!`);
  console.log(`======================================================\n`);
});
