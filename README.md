# ojslab - Journal System (native Go)

Original, from-scratch journal/workflow system. **Not** the GPL PKP OJS - a native
Go + vanilla-JS implementation built by Rudi & Davy, eligible for separate
copyright / HAKI (Hak Kekayaan Intelektual) / patent filing.

## Why native (not PKP OJS)
PKP Open Journal Systems is GPL-2.0; you cannot claim it as original IP. ojslab
is written from zero (Go stdlib + vanilla JS) so the codebase is original
provenance for IP claims. No GPL/copyleft engine is bundled.

## Stack
- Backend: Go (stdlib net/http), PostgreSQL 17, **SSR** (html/template), scs cookie
  session (pgxstore), gorilla/csrf, bcrypt. Single origin, OWASP-hardened.
- Frontend: static landing on GitHub Pages; the app itself is server-rendered
  (no separate JSON API, no CORS, no client-held tokens).
- Deploy: LXC 206 `23-ojs-jurnal` on serverkita (192.168.1.23 / ::123)
- Routing: landing `journal.inlab.my.id` (Pages) + app `ojslab-api.inlab.my.id`
  (Cloudflare Tunnel -> nginx rproxy 101 -> LXC 206)
- Arch mirror of InovasiLab-LMS: `web/` -> Pages landing, app is one SSR origin.

## Layout
```
web/            static frontend (GitHub Pages)
internal/       Go packages (copy of deploy source at /opt/ojs on 206)
docs/           architecture + IP/provenance notes
```

## MVP scope (accepted)
- Article submission (author uploads manuscript + metadata)
- Review workflow (submitted -> in-review -> accepted/rejected)
- Public archive of published articles
- Contributor/editor auth; cookie session + CSRF (SSR)

## Local / deploy
Backend target lives at `/opt/ojs` inside LXC 206 (systemd `ojslab.service`,
port 8081). Source dir mirrors it. Confirm with GitHub Actions on `main` push.