# ojslab Architecture & IP Provenance

## Network topology (serverkita, PVE 9.2)
```
[GitHub Pages]  journal.rudilab.my.id   (static frontend, `web/`)
       |
[Cloudflare Tunnel + nginx]   rproxy 101 (04-reverse-proxy-serverkita)
       |
LXC 206 23-ojs-jurnal  api.journal.rudilab.my.id -> 192.168.1.23:8081
       |-- PostgreSQL 17 (db `ojs`, user `ojs`)
       |-- systemd ojslab.service (Go binary /opt/ojs/ojslab)
```
- Backend on private bridge vmbr1 (192.168.1.23); exposed only via tunnel/nginx.
- davy@pve granted role `LabVMOp` on `/vms/206` (panel: power/console/config),
  verified by impersonation token. No host/root access.

## Backend design (Go stdlib, no framework)
- `net/http` only; no Gin/Echo/Fiber (mirror InovasiLab-LMS + ujiscan).
- Hand-written SQL on `pgx`; no ORM.
- JWT or HMAC-stateless auth (pattern from ujiscan JWT_SECRET); bcrypt password hashes.
- CORS enabled for the Pages origin; tokens via `Authorization: Bearer`.
- Configuration via env (`DATABASE_URL`, `JWT_SECRET`, `PUBLIC_BASE_URL`).
- schema in `internal/`; migrations are plain numbered SQL.

## MVP data model (draft)
Core entities: user, article (title/abstract/authors/manuscript file/submission-date),
review (editor-assigned reviewer, status, comments/decision), publication (accepted article,
volume/issue, published-date).

Workflow states: `draft` -> `submitted` -> `in_review` -> `accepted` | `rejected` -> `published`.

## IP / HAKI provenance (IMPORTANT)
- Prize everything on originality: code written from scratch, no GPL/copyleft engine
  bundled in the distributed image.
- Third-party libraries: keep a dependency list (LICENSES.md) - pgx (MIT), x/crypto (BSD),
  JWT lib (MIT). None copyleft.
- Document invention in `docs/` (invention disclosure) before filing; keep git history clean
  with clear authorship, and a NOTICE file naming Rudi & Davy as authors/rights-holders.
- Publikasi/SIPO route for a software patent/HKI needs a disclosed invention + provenance
  (who wrote what, when, under what license) - keep `NOTICE` and changelog current.

## Revert / rollback
- LXC 206: `pct destroy 206 --purge` (removes config + volumes + snapshots).
- Routing: remove nginx vhost + cloudflared ingress entry + DNS record.
- ACL: `pveum acl delete /vms/206 -user davy@pve -role LabVMOp`.