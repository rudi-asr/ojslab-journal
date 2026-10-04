# ojslab - Perjalanan (Journey Log)

Catatan kronologis pembangunan sistem jurnal native Go, untuk jejak HAKI/paten dan
handoff antar-agent.

## 2026-10-04 - Topologi beres, SSR + OWASP hardening

**Keputusan arsitektur (alasan):**
- **SSR, bukan SPA+JSON API.** Dipilih karena OWASP ASVS & pola InovasiLab-LMS:
  cookie session HttpOnly/Secure/SameSite (SCS + pgxstore), CSRF token,
  satu origin, tanpa CORS, tanpa token yang bisa dibaca JS. Ini "paling kuat"
  dibanding menempatkan JWT di browser (XSS-stealable) + CORS.
- **Asli / paten-eligible.** Bukan turunan PKP OJS (GPL-2.0). Ditulis dari nol.

**Komponen live:**
| Komponen | Value |
|---|---|
| Repo | github.com/rudi-asr/ojslab-journal (PUBLIC) |
| Backend LXC | 206 `23-ojs-jurnal` @ 192.168.1.23 / ::123 |
| Stack | Go stdlib, html/template(embed), pgx, scs, gorilla/csrf, bcrypt |
| systemd | ojslab.service :8081, source /opt/ojs |
| DB | PostgreSQL 17 (users, articles, review_events, sessions) |
| Frontend landing | journal.inlab.my.id (GitHub Pages) |
| API origin | ojslab-api.inlab.my.id (tunnel -> rproxy nginx -> 206) |
| Akses | davy@pve role LabVMOp di /vms/206 |

**Kontrol keamanan (teruji):**
1. Cookie session HttpOnly+Secure+SameSite + rotasi anti-session-fixation (RenewToken)
2. CSRF di semua state-changing (gorilla/csrf; field `gorilla.csrf.Token`)
3. CSP + security headers (nosniff, DENY, no-referrer, COOP)
4. bcrypt + role rotate/per-role authz (author/editor/admin)
5. Rate-limit login 5/menit (dibuktikan 403 setelah beberapa percobaan)
6. Upload: nama acak, luar webroot, validasi MIME, anti path-traversal
7. Download manuscript editor: attachment, no-store; unauth/anon -> deny

**Endpoint:**
- GET `/` arsip publik (published only)
- GET `/article/{id}` detail
- GET `/login` `POST /login` `POST /logout` (cookie session)
- GET `/submit` `POST /submit` (multipart, session)
- GET `/dashboard` (role-aware)
- POST `/review/{id}` (editor/admin)
- GET `/files/{id}` (editor/admin, download manuscript)

## Jebakan yang dipelajari (lihat skill proxmox-ve-admin)
- Cloudflare Universal SSL wildcard mencakup SATU level -> pakai hostname 1-level.
- Token cloudflare per-zone; verify scope dengan GET /zones dulu.
- gorilla/csrf v1.7 field = `gorilla.csrf.Token` (bukan `_csrf`); csrf.Secure(false)
  saat TLS di edge, session cookie tetap Secure=true.
- scs API: pgxstore.New 1 return; Put no return; PopString 1 return.
- AppleDouble `._*` dari macOS merusak embed migrations -> COPYFILE_DISABLE=1 + rebuild.

## Status tersisa (belum produksi)
- Registrasi penulis publik belum ada.
- Akun editor contoh editor@ojslab.test (ganti ke email nyata).
- OJS_EDITOR_PASSWORD masih di unit systemd (pindah ke secret file/manager).
- OMP (publisher buku) nanti ko-instal di LXC 206 yang sama.