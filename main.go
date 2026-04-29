package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
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

var (
	db     *sql.DB
	pgMode bool
)

// ph returns a SQL placeholder: $N for PostgreSQL, ? for SQLite.
func ph(n int) string {
	if pgMode {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")

	var err error
	if dbURL != "" {
		pgMode = true
		db, err = sql.Open("postgres", dbURL)
		if err != nil {
			log.Fatal("DB open (postgres):", err)
		}
		log.Println("Using PostgreSQL")
	} else {
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = "./freecell.db"
		}
		db, err = sql.Open("sqlite", dbPath)
		if err != nil {
			log.Fatal("DB open (sqlite):", err)
		}
		db.SetMaxOpenConns(1)
		log.Println("Using SQLite:", dbPath)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatal("DB ping:", err)
	}

	if err = migrate(); err != nil {
		log.Fatal("Migrate:", err)
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

func migrate() error {
	var createTable string
	if pgMode {
		createTable = `
			CREATE TABLE IF NOT EXISTS records (
				id          SERIAL PRIMARY KEY,
				player_name TEXT    NOT NULL,
				deal_number INTEGER NOT NULL,
				time_secs   INTEGER NOT NULL,
				moves       INTEGER NOT NULL,
				created_at  TEXT    NOT NULL
			)`
	} else {
		createTable = `
			CREATE TABLE IF NOT EXISTS records (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				player_name TEXT    NOT NULL,
				deal_number INTEGER NOT NULL,
				time_secs   INTEGER NOT NULL,
				moves       INTEGER NOT NULL,
				created_at  TEXT    NOT NULL
			)`
	}
	if _, err := db.Exec(createTable); err != nil {
		return err
	}
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_time ON records(time_secs)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_deal ON records(deal_number, time_secs)`)
	return nil
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
	dateStr := r.URL.Query().Get("date") // YYYY-MM-DD: 해당 날짜 플레이어별 최고기록
	fromStr := r.URL.Query().Get("from") // YYYY-MM-DD: 이 날짜 이후 날짜별·플레이어별 최고기록
	limit := 100

	if dateStr != "" {
		if _, err := time.Parse("2006-01-02", dateStr); err != nil {
			http.Error(w, "invalid date format, use YYYY-MM-DD", http.StatusBadRequest)
			return
		}
	}
	toStr := r.URL.Query().Get("to") // UTC ISO timestamp: 이 시각 이전

	if fromStr != "" {
		// YYYY-MM-DD 또는 full ISO timestamp 허용
		if !strings.Contains(fromStr, "T") {
			if _, err := time.Parse("2006-01-02", fromStr); err != nil {
				http.Error(w, "invalid from format", http.StatusBadRequest)
				return
			}
			fromStr = fromStr + "T00:00:00Z"
		}
	}
	if toStr != "" {
		if !strings.Contains(toStr, "T") {
			if _, err := time.Parse("2006-01-02", toStr); err != nil {
				http.Error(w, "invalid to format", http.StatusBadRequest)
				return
			}
			toStr = toStr + "T23:59:59Z"
		}
	}

	var (
		rows *sql.Rows
		err  error
	)

	if dateStr != "" {
		// Daily leaderboard: best record per player for the given date
		// created_at is stored as ISO 8601 text (e.g. "2026-04-23T03:30:00Z")
		// Use LIKE 'YYYY-MM-DD%' to match the date prefix — works in both SQLite and PostgreSQL (TEXT column)
		datePrefix := dateStr + "%"
		p := func(n int) string { return ph(n) }

		var dealCond string
		var args []any
		if dealStr != "" {
			deal, e := strconv.Atoi(dealStr)
			if e != nil || deal < 1 || deal > 32000 {
				http.Error(w, "invalid dealNumber", http.StatusBadRequest)
				return
			}
			dealCond = fmt.Sprintf("AND deal_number = %s", p(2))
			args = []any{datePrefix, deal, limit}
		} else {
			dealCond = ""
			args = []any{datePrefix, limit}
		}

		limitPh := p(len(args))
		rows, err = db.Query(fmt.Sprintf(`
			SELECT id, player_name, deal_number, time_secs, moves, created_at
			FROM (
				SELECT *,
				       ROW_NUMBER() OVER (
				           PARTITION BY player_name
				           ORDER BY time_secs ASC, moves ASC, id ASC
				       ) AS rn
				FROM records
				WHERE created_at LIKE %s %s
			) sub
			WHERE rn = 1
			ORDER BY time_secs ASC, moves ASC
			LIMIT %s
		`, p(1), dealCond, limitPh), args...)
	} else if fromStr != "" {
		var toCond string
		var args []any
		if toStr != "" {
			toCond = fmt.Sprintf("AND created_at <= %s", ph(2))
			args = []any{fromStr, toStr, limit}
		} else {
			args = []any{fromStr, limit}
		}
		limitPh := ph(len(args))
		rows, err = db.Query(fmt.Sprintf(`
			SELECT id, player_name, deal_number, time_secs, moves, created_at
			FROM (
				SELECT *,
				       ROW_NUMBER() OVER (
				           PARTITION BY player_name, substr(created_at, 1, 10)
				           ORDER BY time_secs ASC, moves ASC, id ASC
				       ) AS rn
				FROM records
				WHERE created_at >= %s %s
			) sub
			WHERE rn = 1
			ORDER BY time_secs ASC, moves ASC
			LIMIT %s
		`, ph(1), toCond, limitPh), args...)
	} else if dealStr != "" {
		deal, e := strconv.Atoi(dealStr)
		if e != nil || deal < 1 || deal > 32000 {
			http.Error(w, "invalid dealNumber", http.StatusBadRequest)
			return
		}
		rows, err = db.Query(fmt.Sprintf(`
			SELECT id, player_name, deal_number, time_secs, moves, created_at
			FROM (
				SELECT *,
				       ROW_NUMBER() OVER (
				           PARTITION BY player_name, substr(created_at, 1, 10)
				           ORDER BY time_secs ASC, moves ASC, id ASC
				       ) AS rn
				FROM records
				WHERE deal_number = %s
			) sub
			WHERE rn = 1
			ORDER BY time_secs ASC, moves ASC
			LIMIT %s
		`, ph(1), ph(2)), deal, limit)
	} else {
		rows, err = db.Query(fmt.Sprintf(`
			SELECT id, player_name, deal_number, time_secs, moves, created_at
			FROM (
				SELECT *,
				       ROW_NUMBER() OVER (
				           PARTITION BY player_name, substr(created_at, 1, 10)
				           ORDER BY time_secs ASC, moves ASC, id ASC
				       ) AS rn
				FROM records
			) sub
			WHERE rn = 1
			ORDER BY time_secs ASC, moves ASC
			LIMIT %s
		`, ph(1)), limit)
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
	today := now[:10] + "%" // YYYY-MM-DD%

	// 오늘 이미 등록된 기록 확인
	var existingID int64
	var existingTime, existingMoves int
	err := db.QueryRow(fmt.Sprintf(`
		SELECT id, time_secs, moves FROM records
		WHERE player_name = %s AND created_at LIKE %s
		ORDER BY time_secs ASC, moves ASC
		LIMIT 1
	`, ph(1), ph(2)), name, today).Scan(&existingID, &existingTime, &existingMoves)

	if err != nil && err != sql.ErrNoRows {
		log.Println("query existing:", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	hasExisting := err != sql.ErrNoRows
	if hasExisting {
		// 기존 기록보다 좋지 않으면 저장하지 않음
		if req.Time > existingTime || (req.Time == existingTime && req.Moves >= existingMoves) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"id": existingID, "skipped": true})
			return
		}
		// 더 좋은 기록 → 오늘 기록 전체 삭제 후 새로 등록
		if _, err := db.Exec(fmt.Sprintf(`
			DELETE FROM records WHERE player_name = %s AND created_at LIKE %s
		`, ph(1), ph(2)), name, today); err != nil {
			log.Println("delete existing:", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
	}

	var id int64
	if pgMode {
		err := db.QueryRow(fmt.Sprintf(`
			INSERT INTO records (player_name, deal_number, time_secs, moves, created_at)
			VALUES (%s, %s, %s, %s, %s)
			RETURNING id
		`, ph(1), ph(2), ph(3), ph(4), ph(5)),
			name, req.DealNumber, req.Time, req.Moves, now,
		).Scan(&id)
		if err != nil {
			log.Println("insert:", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
	} else {
		res, err := db.Exec(fmt.Sprintf(`
			INSERT INTO records (player_name, deal_number, time_secs, moves, created_at)
			VALUES (%s, %s, %s, %s, %s)
		`, ph(1), ph(2), ph(3), ph(4), ph(5)),
			name, req.DealNumber, req.Time, req.Moves, now,
		)
		if err != nil {
			log.Println("insert:", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		id, _ = res.LastInsertId()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": id})
}
