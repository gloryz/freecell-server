package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Record struct {
	ID         int64  `json:"id"`
	PlayerName string `json:"playerName"`
	DealNumber int    `json:"dealNumber"`
	Time       int    `json:"time"`
	Moves      int    `json:"moves"`
	Date       string `json:"date"`
	Rank       int    `json:"rank,omitempty"`
}

var db *sql.DB

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./freecell.db"
	}

	var err error
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal("DB open:", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1) // SQLite single-writer

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS records (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			player_name TEXT    NOT NULL,
			deal_number INTEGER NOT NULL,
			time_secs   INTEGER NOT NULL,
			moves       INTEGER NOT NULL,
			created_at  TEXT    NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_time ON records(time_secs);
		CREATE INDEX IF NOT EXISTS idx_deal ON records(deal_number, time_secs);
	`)
	if err != nil {
		log.Fatal("Create table:", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/records", handleRecords)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("FreeCell server listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, cors(mux)))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		getRecords(w, r)
	case http.MethodPost:
		postRecord(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func getRecords(w http.ResponseWriter, r *http.Request) {
	dealStr := r.URL.Query().Get("dealNumber")
	limit := 100

	var (
		rows *sql.Rows
		err  error
	)
	if dealStr != "" {
		deal, e := strconv.Atoi(dealStr)
		if e != nil || deal < 1 || deal > 32000 {
			http.Error(w, "invalid dealNumber", http.StatusBadRequest)
			return
		}
		rows, err = db.Query(`
			SELECT id, player_name, deal_number, time_secs, moves, created_at
			FROM records
			WHERE deal_number = ?
			ORDER BY time_secs ASC, moves ASC
			LIMIT ?
		`, deal, limit)
	} else {
		rows, err = db.Query(`
			SELECT id, player_name, deal_number, time_secs, moves, created_at
			FROM records
			ORDER BY time_secs ASC, moves ASC
			LIMIT ?
		`, limit)
	}
	if err != nil {
		log.Println("query:", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	records := make([]Record, 0)
	rank := 1
	for rows.Next() {
		var rec Record
		if err := rows.Scan(&rec.ID, &rec.PlayerName, &rec.DealNumber, &rec.Time, &rec.Moves, &rec.Date); err != nil {
			continue
		}
		rec.Rank = rank
		rank++
		records = append(records, rec)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"records": records})
}

func postRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlayerName string `json:"playerName"`
		DealNumber int    `json:"dealNumber"`
		Time       int    `json:"time"`
		Moves      int    `json:"moves"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.PlayerName)
	if name == "" || len(name) > 20 {
		http.Error(w, "playerName must be 1–20 chars", http.StatusBadRequest)
		return
	}
	if req.DealNumber < 1 || req.DealNumber > 32000 {
		http.Error(w, "dealNumber out of range", http.StatusBadRequest)
		return
	}
	if req.Time < 5 || req.Time > 7200 {
		http.Error(w, "time out of range", http.StatusBadRequest)
		return
	}
	if req.Moves < 1 || req.Moves > 10000 {
		http.Error(w, "moves out of range", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(`
		INSERT INTO records (player_name, deal_number, time_secs, moves, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, name, req.DealNumber, req.Time, req.Moves, now)
	if err != nil {
		log.Println("insert:", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": id})
}
