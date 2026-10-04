package main

import (
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/alexedwards/scs/v2"
	"golang.org/x/crypto/bcrypt"
)

type server struct {
	pool      *pgxpool.Pool
	sessions  *scs.SessionManager
	dataDir   string
	templates map[string]*template.Template
	rl        *rateLimiter
}

func loadTemplates() (map[string]*template.Template, error) {
	out := make(map[string]*template.Template)
	base, err := embeddedFS.ReadFile("templates/layout.html")
	if err != nil {
		return nil, err
	}
	names := []string{"archive", "article", "submit", "login", "dashboard"}
	for _, n := range names {
		page, err := embeddedFS.ReadFile("templates/" + n + ".html")
		if err != nil {
			return nil, err
		}
		t, err := template.New("base").Parse(string(base) + "\n" + string(page))
		if err != nil {
			return nil, err
		}
		out[n] = t
	}
	return out, nil
}

func (s *server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	t, ok := s.templates[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	if _, ok := data["Title"]; !ok {
		data["Title"] = ""
	}
	data["User"] = s.currentUser(r)
	data["CSRF"] = csrf.TemplateField(r)
	if msg := s.sessions.PopString(r.Context(), "flash"); msg != "" {
		data["Flash"] = msg
	}
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func (s *server) flash(r *http.Request, msg string) {
	s.sessions.Put(r.Context(), "flash", msg)
}

// ---------- auth helpers ----------
func (s *server) currentUser(r *http.Request) *User {
	id, ok := s.sessions.Get(r.Context(), "user_id").(int64)
	if !ok {
		return nil
	}
	var u User
	err := s.pool.QueryRow(r.Context(),
		`SELECT id,email,name,role FROM users WHERE id=$1`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role)
	if err != nil {
		return nil
	}
	return &u
}

func (s *server) requireGuest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.currentUser(r) != nil {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *server) loginRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.currentUser(r) == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *server) editorRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.Role != "editor" && u.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// ---------- public ----------
func (s *server) handleArchive(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT id,title,abstract,author,status,volume,issue,
		        to_char(published_at,'YYYY-MM-DD'),COALESCE('','')
		 FROM articles WHERE status='published' ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var arts []Article
	for rows.Next() {
		var a Article
		var p *string
		if err := rows.Scan(&a.ID, &a.Title, &a.Abstract, &a.Author, &a.Status,
			&a.Volume, &a.Issue, &p, &a.ReviewNote); err != nil {
			continue
		}
		a.PublishedDate = p
		arts = append(arts, a)
	}
	s.render(w, r, "archive", map[string]any{"Articles": arts})
}

func (s *server) handleArticleView(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var a Article
	var p *string
	err = s.pool.QueryRow(r.Context(),
		`SELECT id,title,abstract,author,status,volume,issue,to_char(published_at,'YYYY-MM-DD')
		 FROM articles WHERE id=$1`, id).
		Scan(&a.ID, &a.Title, &a.Abstract, &a.Author, &a.Status, &a.Volume, &a.Issue, &p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.PublishedDate = p
	s.render(w, r, "article", map[string]any{"A": a})
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	b, err := embeddedFS.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	_, _ = w.Write(b)
}

// ---------- auth handlers ----------
func (s *server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login", nil)
}

func (s *server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	// rate limit by IP + email
	if !s.rl.Allow(r.RemoteAddr + "|" + r.FormValue("email")) {
		http.Error(w, "too many attempts, slow down", http.StatusTooManyRequests)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	pw := r.FormValue("password")
	var u User
	var hash string
	err := s.pool.QueryRow(r.Context(),
		`SELECT id,email,name,role,password_hash FROM users WHERE email=$1`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		s.flash(r, "Email atau password salah")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// session rotation (anti fixation)
	_ = s.sessions.RenewToken(r.Context())
	s.sessions.Put(r.Context(), "user_id", u.ID)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	_ = s.sessions.Destroy(r.Context())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---------- submission ----------
func (s *server) submitForm(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	s.render(w, r, "submit", map[string]any{"AuthorEmail": u.Email, "AuthorName": u.Name})
}

func (s *server) submitCreate(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		s.flash(r, "form tidak valid")
		http.Redirect(w, r, "/submit", http.StatusSeeOther)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	abstract := strings.TrimSpace(r.FormValue("abstract"))
	author := strings.TrimSpace(r.FormValue("author"))
	if title == "" || abstract == "" || author == "" {
		s.flash(r, "judul, abstrak, dan penulis wajib diisi")
		http.Redirect(w, r, "/submit", http.StatusSeeOther)
		return
	}
	f, h, err := r.FormFile("manuscript")
	if err != nil {
		s.flash(r, "file naskah wajib ada")
		http.Redirect(w, r, "/submit", http.StatusSeeOther)
		return
	}
	defer f.Close()
	ct := h.Header.Get("Content-Type")
	ext := strings.ToLower(filepath.Ext(h.Filename))
	if !extAllowed(ct, ext) {
		s.flash(r, "naskah harus PDF atau DOC/DOCX")
		http.Redirect(w, r, "/submit", http.StatusSeeOther)
		return
	}
	// store outside webroot, random name
	rand := randHex(12)
	ext2 := strings.TrimPrefix(ext, ".")
	if ext2 != "pdf" && ext2 != "doc" && ext2 != "docx" {
		ext2 = "pdf"
	}
	rel := "submissions/" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + rand + "." + ext2
	abs := filepath.Join(s.dataDir, rel)
	dst, err := os.Create(abs)
	if err != nil {
		s.flash(r, "gagal menyimpan naskah")
		http.Redirect(w, r, "/submit", http.StatusSeeOther)
		return
	}
	if _, err := io.Copy(dst, f); err != nil {
		dst.Close()
		s.flash(r, "gagal menulis naskah")
		http.Redirect(w, r, "/submit", http.StatusSeeOther)
		return
	}
	dst.Close()

	var id int64
	err = s.pool.QueryRow(r.Context(),
		`INSERT INTO articles(title,abstract,author,email,manuscript_path,status,created_by)
		 VALUES($1,$2,$3,$4,$5,'submitted',$6) RETURNING id`,
		title, abstract, author, u.Email, rel, u.ID).Scan(&id)
	if err != nil {
		log.Printf("insert article: %v", err)
		s.flash(r, "gagal menyimpan submission")
		http.Redirect(w, r, "/submit", http.StatusSeeOther)
		return
	}
	_, _ = s.pool.Exec(r.Context(),
		`INSERT INTO review_events(article_id,from_status,to_status,note,by_email)
		 VALUES($1,'','submitted','author submission received',$2)`, id, u.Email)
	s.flash(r, "Submission diterima (ID "+strconv.FormatInt(id, 10)+")")
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// ---------- dashboard ----------
func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(r)
	if u.Role == "author" {
		rows, err := s.pool.Query(r.Context(),
			`SELECT id,title,abstract,author,email,status FROM articles
			 WHERE created_by=$1 ORDER BY created_at DESC`, u.ID)
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		var mine []Article
		for rows.Next() {
			var a Article
			if rows.Scan(&a.ID, &a.Title, &a.Abstract, &a.Author, &a.Email, &a.Status) == nil {
				mine = append(mine, a)
			}
		}
		s.render(w, r, "dashboard", map[string]any{"Mine": mine})
		return
	}
	// editor/admin: all articles for review
	rows, err := s.pool.Query(r.Context(),
		`SELECT id,title,abstract,author,email,status,manuscript_path IS NOT NULL
		 FROM articles ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var all []Article
	for rows.Next() {
		var a Article
		if rows.Scan(&a.ID, &a.Title, &a.Abstract, &a.Author, &a.Email, &a.Status, &a.HasFile) == nil {
			all = append(all, a)
		}
	}
	s.render(w, r, "dashboard", map[string]any{"All": all})
}

// ---------- review ----------
func (s *server) review(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	to := r.FormValue("decision")
	note := strings.TrimSpace(r.FormValue("note"))
	switch to {
	case "in_review", "accepted", "rejected", "published":
	default:
		http.Error(w, "invalid decision", http.StatusBadRequest)
		return
	}
	var from string
	if err := s.pool.QueryRow(r.Context(), `SELECT status FROM articles WHERE id=$1`, id).Scan(&from); err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = s.pool.Exec(r.Context(),
		`UPDATE articles SET status=$2,
		   published_at = CASE WHEN $2='published' THEN now() ELSE published_at END
		 WHERE id=$1`, id, to)
	u := s.currentUser(r)
	_, _ = s.pool.Exec(r.Context(),
		`INSERT INTO review_events(article_id,from_status,to_status,note,by_email)
		 VALUES($1,$2,$3,$4,$5)`, id, from, to, note, u.Email)
	s.flash(r, "Status artikel #"+strconv.FormatInt(id, 10)+" -> "+to)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

// ---------- serve manuscript (editor/admin) ----------
func (s *server) serveFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var path string
	err = s.pool.QueryRow(r.Context(),
		`SELECT manuscript_path FROM articles WHERE id=$1 AND manuscript_path IS NOT NULL`, id).Scan(&path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// resolve within dataDir only; block traversal
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, "../") || filepath.IsAbs(clean) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(s.dataDir, clean)
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	name := filepath.Base(clean)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("Content-Type", contentTypeFor(name))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}

func contentTypeFor(name string) string {
	switch filepath.Ext(name) {
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}
	return "application/octet-stream"
}

// ---------- middleware ----------
func (s *server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *server) rateLimitLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" && r.Method == http.MethodPost {
			if !s.rl.Allow(r.RemoteAddr) {
				http.Error(w, "too many attempts", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}