package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ojslab - native Go journal backend. stdlib net/http, pgx, hand SQL.
// MVP: submission (multipart), public archive, review workflow.

var allowedMIME = map[string]bool{
	"application/pdf":       true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/msword": true,
}

type server struct {
	pool        *pgxpool.Pool
	dataDir     string
	editorToken string // simplistic editor auth for MVP (shared secret via header)
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
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	if err := runMigrations(ctx, pool); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	dataDir := os.Getenv("OJS_DATA")
	if dataDir == "" {
		dataDir = "/opt/ojs/data"
	}
	os.MkdirAll(dataDir, 0o755)

	s := &server{
		pool:        pool,
		dataDir:     dataDir,
		editorToken: os.Getenv("OJS_EDITOR_TOKEN"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/v1/health", s.apiHealth)
	mux.HandleFunc("GET /api/v1/articles", s.listArticles)    // ?status=published (public)
	mux.HandleFunc("POST /api/v1/submissions", s.createSubmission)
	mux.HandleFunc("POST /api/v1/articles/{id}/review", s.updateStatus) // editor action

	addr := ":8081"
	log.Printf("ojslab backend listening on %s (data=%s)", addr, dataDir)
	log.Fatal(http.ListenAndServe(addr, withCommonHeaders(mux)))
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		return err
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if len(b) == 0 {
			continue
		}
		if _, err := pool.Exec(ctx, string(b)); err != nil {
			// tolerate "already exists" during idempotent re-run
			return err
		}
	}
	return nil
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		http.Error(w, `{"status":"db_down"}`, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "db": "up"})
}

func (s *server) apiHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "ojslab-journal", "version": "0.1.0",
		"time": time.Now().Format(time.RFC3339),
	})
}

type article struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	Abstract     string    `json:"abstract"`
	Author       string    `json:"author"`
	Status       string    `json:"status"`
	Volume       *string   `json:"volume"`
	Issue        *string   `json:"issue"`
	PublishedDate *string  `json:"published_date"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *server) listArticles(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	q := `SELECT id,title,abstract,author,status,volume,issue,
	          to_char(published_at,'YYYY-MM-DD'),created_at
	       FROM articles`
	args := []any{}
	if status != "" {
		q += ` WHERE status=$1`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.pool.Query(r.Context(), q, args...)
	if err != nil {
		http.Error(w, `{"message":"query failed"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var arts []article
	for rows.Next() {
		var a article
		var vol, iss *string
		var pub *string
		if err := rows.Scan(&a.ID, &a.Title, &a.Abstract, &a.Author, &a.Status,
			&vol, &iss, &pub, &a.CreatedAt); err != nil {
			log.Printf("scan article: %v", err)
			continue
		}
		a.Volume, a.Issue, a.PublishedDate = vol, iss, pub
		arts = append(arts, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"articles": arts})
}

func (s *server) createSubmission(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	abstract := strings.TrimSpace(r.FormValue("abstract"))
	author := strings.TrimSpace(r.FormValue("author"))
	email := strings.TrimSpace(r.FormValue("email"))
	if title == "" || abstract == "" || author == "" || email == "" {
		writeErr(w, http.StatusBadRequest, "title, abstract, author, email required")
		return
	}

	f, h, err := r.FormFile("manuscript")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "manuscript file required")
		return
	}
	defer f.Close()
	ct := h.Header.Get("Content-Type")
	if !allowedMIME[ct] {
		writeErr(w, http.StatusBadRequest, "manuscript must be PDF or DOCX")
		return
	}
	// sanitize filename extension
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(h.Filename), "."))
	if ext != "pdf" && ext != "docx" && ext != "doc" {
		ext = "pdf"
	}
	rel := "submissions/" + strconv.FormatInt(time.Now().UnixNano(), 10) + "." + ext
	abs := filepath.Join(s.dataDir, rel)
	os.MkdirAll(filepath.Join(s.dataDir, "submissions"), 0o755)
	dst, err := os.Create(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not store manuscript")
		return
	}
	if _, err := io.Copy(dst, f); err != nil {
		dst.Close()
		writeErr(w, http.StatusInternalServerError, "could not write manuscript")
		return
	}
	dst.Close()

	var id int64
	err = s.pool.QueryRow(r.Context(),
		`INSERT INTO articles(title,abstract,author,email,manuscript_path,status)
		 VALUES($1,$2,$3,$4,$5,'submitted') RETURNING id`,
		title, abstract, author, email, rel).Scan(&id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not insert submission")
		return
	}
	s.logReview(r, id, "", "submitted", "author submission received")
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "status": "submitted",
		"message": "submission accepted; awaiting review",
	})
}

// updateStatus moves an article through the review workflow. Authorized by
// editor shared secret (X-OJS-Editor header) for MVP; NOT production auth.
func (s *server) updateStatus(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if s.editorToken == "" || r.Header.Get("X-OJS-Editor") != s.editorToken {
		writeErr(w, http.StatusUnauthorized, "editor auth required")
		return
	}
	var req struct {
		To   string `json:"to"`
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if !validStatusTransition(req.To) {
		writeErr(w, http.StatusBadRequest, "invalid target status")
		return
	}
	var from string
	err = s.pool.QueryRow(r.Context(),
		`SELECT status FROM articles WHERE id=$1`, id).Scan(&from)
	if err != nil {
		writeErr(w, http.StatusNotFound, "article not found")
		return
	}
	_, err = s.pool.Exec(r.Context(),
		`UPDATE articles SET status=$2,
		   published_at = CASE WHEN $2='published' THEN now() ELSE published_at END
		   WHERE id=$1`,
		id, req.To)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	s.logReview(r, id, from, req.To, req.Note)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "from": from, "to": req.To})
}

func validStatusTransition(to string) bool {
	switch to {
	case "in_review", "accepted", "rejected", "published":
		return true
	}
	return false
}

func (s *server) logReview(r *http.Request, articleID int64, from, to, note string) {
	_, _ = s.pool.Exec(r.Context(),
		`INSERT INTO review_events(article_id,from_status,to_status,note,by_email)
		 VALUES($1,$2,$3,$4,$5)`,
		articleID, from, to, note, r.Header.Get("X-OJS-Email"))
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"message": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-OJS-Editor, X-OJS-Email")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}