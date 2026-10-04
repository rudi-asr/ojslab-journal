/* ojslab frontend - talks to backend API via CORS.
   API base is configurable: localhost for dev, public tunnel for prod. */
const API_BASE =
  (typeof OJSLAB_API_BASE !== "undefined" && OJSLAB_API_BASE) ||
  (location.hostname === "localhost" || location.hostname.endsWith("github.io"))
    ? "http://localhost:8081"
    : "https://ojslab-api.inlab.my.id";

/* ---------- public archive (index.html) ---------- */
const loadArchive = async () => {
  const el = document.getElementById("articles");
  if (!el) return;
  try {
    const res = await fetch(`${API_BASE}/api/v1/articles?status=published`);
    if (!res.ok) throw new Error("HTTP " + res.status);
    const data = await res.json();
    const list = data.articles || [];
    if (!list.length) {
      el.innerHTML = "<p>Tidak ada publikasi.</p>";
      return;
    }
    el.innerHTML = list.map(a => `
      <article class="article-card">
        <h3>${escapeHtml(a.title)}</h3>
        <p class="meta">${escapeHtml(a.author)} &middot; ${escapeHtml(a.published_date || "")}</p>
        <span class="status published">Published</span>
      </article>`).join("");
  } catch (err) {
    el.innerHTML = `<p>Gagal memuat arsip (${escapeHtml(String(err.message))}). Backend offline?</p>`;
  }
};

/* ---------- submit (submit.html) ---------- */
const bindSubmit = () => {
  const form = document.getElementById("submitForm");
  if (!form) return;
  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const out = document.getElementById("result");
    const fd = new FormData(form);
    out.textContent = "Mengirim...";
    try {
      const res = await fetch(`${API_BASE}/api/v1/submissions`, {
        method: "POST", body: fd,
      });
      const data = await res.json();
      out.textContent = res.ok
        ? `Submission diterima (ID ${data.id}). Status: ${data.status}`
        : `Error: ${esc(data.message || res.status)}`;
    } catch (err) {
      out.textContent = `Gagal: ${esc(String(err.message))}`;
    }
  });
};

const esc = (s) => String(s).replace(/[&<>"]/g, c =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const escapeHtml = esc;

document.addEventListener("DOMContentLoaded", () => { loadArchive(); bindSubmit(); });