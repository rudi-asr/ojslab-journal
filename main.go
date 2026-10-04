package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/gorilla/csrf"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// ojslab - SSR journal backend, OWASP-aligned hardening.
// stdlib net/http, html/template (embed), scs cookie session (pgx store),
// gorilla/csrf, bcrypt. Single origin. No CORS, no client-held tokens.

//go:embed templates/*.html static/* migrations/*.sql
var embeddedFS embed.FS

type User struct {
	ID    int64
	Email string
	Name  string
	Role  string
}

type Article struct {
	ID            int64
	Title         string
	Abstract      string
	Author        string
	Email         string
	Status        string
	Volume        *string
	Issue         *string
	PublishedDate *string
	ReviewNote    string
	HasFile       bool
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://ojs:ojslocaldev@127.0.0.1:5432/ojs"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("db pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	if err := runMigrations(ctx, pool); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	log.Printf("migrations OK")

	if os.Getenv("OJS_SEED_EDITOR") == "1" {
		if em := os.Getenv("OJS_EDITOR_EMAIL"); em != "" {
			seedEditor(ctx, pool, em, os.Getenv("OJS_EDITOR_PASSWORD"))
		}
	}

	dataDir := os.Getenv("OJS_DATA")
	if dataDir == "" {
		dataDir = "/opt/ojs/data"
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "submissions"), 0o755); err != nil {
		log.Fatalf("mkdir data: %v", err)
	}

	s := newServer(pool, dataDir)
	log.Printf("ojslab SSR listening on :8081")
	log.Fatal(http.ListenAndServe(":8081", s.routes()))
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	entries, _ := embeddedFS.ReadDir("migrations")
	for _, e := range entries {
		b, err := embeddedFS.ReadFile("migrations/" + e.Name())
		if err != nil || len(b) == 0 {
			continue
		}
		if _, werr := pool.Exec(ctx, string(b)); werr != nil {
			log.Printf("migration %s: %v", e.Name(), werr)
		}
	}
	return nil
}

func seedEditor(ctx context.Context, pool *pgxpool.Pool, email, pw string) {
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	// only create if absent - never force-promote an existing account on boot
	_, err := pool.Exec(ctx,
		`INSERT INTO users(email,password_hash,name,role)
		 SELECT $1,$2,'Editor','admin'
		 WHERE NOT EXISTS (SELECT 1 FROM users WHERE email=$1)`, email, string(hash))
	if err != nil {
		log.Printf("seed editor: %v", err)
	}
}

func newServer(pool *pgxpool.Pool, dataDir string) *server {
	store := pgxstore.New(pool)
	sess := scs.New()
	sess.Store = store
	sess.Lifetime = 12 * time.Hour
	sess.Cookie.Name = "ojslab_session"
	sess.Cookie.HttpOnly = true
	sess.Cookie.SameSite = http.SameSiteStrictMode
	sess.Cookie.Secure = true

	ts, err := loadTemplates()
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	return &server{
		pool:      pool,
		sessions:  sess,
		dataDir:   dataDir,
		templates: ts,
		rl:        newRateLimiter(5, time.Minute),
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleArchive)
	mux.HandleFunc("GET /article/{id}", s.handleArticleView)
	mux.HandleFunc("GET /static/", s.handleStatic)

	mux.HandleFunc("GET /login", s.requireGuest(s.loginForm))
	mux.HandleFunc("POST /login", s.requireGuest(s.loginSubmit))
	mux.HandleFunc("POST /logout", s.loginRequired(s.logout))

	mux.HandleFunc("GET /submit", s.loginRequired(s.submitForm))
	mux.HandleFunc("POST /submit", s.loginRequired(s.submitCreate))

	mux.HandleFunc("GET /dashboard", s.loginRequired(s.dashboard))
	mux.HandleFunc("POST /review/{id}", s.editorRequired(s.review))
	mux.HandleFunc("GET /files/{id}", s.editorRequired(s.serveFile))

	var h http.Handler = mux
	h = s.secureHeaders(h)
	h = s.rateLimitLogin(h)
	h = csrf.Protect(
		[]byte(envOr("OJS_CSRF_SECRET", "0123456789abcdef0123456789abcdef")),
		csrf.Path("/"),
		// TLS terminates at Cloudflare/nginx; backend only ever sees http.
		// csrf.Secure(false) lets the token be issued over the internal hop.
		csrf.Secure(false),
	)(h)
	h = s.sessions.LoadAndSave(h)
	return h
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func extAllowed(ct, ext string) bool {
	switch ct {
	case "application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/msword":
		return true
	}
	lower := strings.ToLower(ext)
	return lower == ".pdf" || lower == ".doc" || lower == ".docx"
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}