package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// ServeHTTP demonstrates a plugin that handles HTTP requests by greeting the world.
// The root URL is currently <siteUrl>/plugins/com.mattermost.plugin-starter-template/api/v1/. Replace com.mattermost.plugin-starter-template with the plugin ID.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	router := mux.NewRouter()

	publicRouter := router.PathPrefix("/api/v1").Subrouter()
	publicRouter.HandleFunc("/test", p.TestDatabase).Methods(http.MethodGet)
	publicRouter.HandleFunc("/test_raw", p.TestDatabaseRaw).Methods(http.MethodGet)

	// Protected routes
	secureRouter := router.PathPrefix("/api/v1").Subrouter()
	secureRouter.Use(p.MattermostAuthorizationRequired)
	secureRouter.HandleFunc("/hello", p.HelloWorld).Methods(http.MethodGet)

	router.ServeHTTP(w, r)
}

func (p *Plugin) MattermostAuthorizationRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("Mattermost-User-ID")
		if userID == "" {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (p *Plugin) HelloWorld(w http.ResponseWriter, r *http.Request) {
	if _, err := w.Write([]byte("Hello, world!")); err != nil {
		p.API.LogError("Failed to write response", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type TestResult struct {
	InsertTimeSeconds     float64 `json:"insert_time_seconds"`
	TotalQueryTimeSeconds float64 `json:"total_query_time_seconds"`
	Error                 string  `json:"error,omitempty"`
	ConnType              string  `json:"conn_type"`
	RecordsQueried        int     `json:"records_queried"`
	PageSize              int     `json:"page_size"`
}

// TestDatabase uses the StoreService to access the Mattermost database
func (p *Plugin) TestDatabase(w http.ResponseWriter, r *http.Request) {
	// Parse page size from query param
	pageSize := 100 // Default page size
	pageSizeParam := r.URL.Query().Get("page_size")
	if pageSizeParam != "" {
		if size, err := strconv.Atoi(pageSizeParam); err == nil && size > 0 {
			pageSize = size
		}
	}

	// Get database from StoreService
	store := p.client.Store
	db, err := store.GetMasterDB()
	if err != nil {
		p.API.LogError("Failed to get database", "error", err)
		respondWithJSON(w, http.StatusInternalServerError, TestResult{
			Error:    fmt.Sprintf("Failed to get database: %v", err),
			ConnType: "rpc",
		})
		return
	}

	// Run test through helper method
	result, err := p.runDatabaseTest(db, store.DriverName(), pageSize)
	if err != nil {
		p.API.LogError("Test failed", "error", err)
		respondWithJSON(w, http.StatusInternalServerError, TestResult{
			Error:    err.Error(),
			ConnType: "rpc",
		})
		return
	}

	// Set connection type
	result.ConnType = "rpc"

	respondWithJSON(w, http.StatusOK, result)
}

// TestDatabaseRaw establishes a direct connection to the database using config
func (p *Plugin) TestDatabaseRaw(w http.ResponseWriter, r *http.Request) {
	// Parse page size from query param
	pageSize := 100 // Default page size
	pageSizeParam := r.URL.Query().Get("page_size")
	if pageSizeParam != "" {
		if size, err := strconv.Atoi(pageSizeParam); err == nil && size > 0 {
			pageSize = size
		}
	}

	// Get unsanitized config to access database credentials
	config := p.API.GetUnsanitizedConfig()
	if config == nil {
		respondWithJSON(w, http.StatusInternalServerError, TestResult{
			Error:    "Failed to get server configuration",
			ConnType: "raw",
		})
		return
	}

	var db *sql.DB
	var err error
	var driverName string

	// Connect based on database type
	switch *config.SqlSettings.DriverName {
	case model.DatabaseDriverMysql:
		driverName = "mysql"
		dataSource := *config.SqlSettings.DataSource
		db, err = sql.Open(driverName, dataSource)
	case model.DatabaseDriverPostgres:
		driverName = "postgres"
		dataSource := *config.SqlSettings.DataSource
		db, err = sql.Open(driverName, dataSource)
	default:
		respondWithJSON(w, http.StatusInternalServerError, TestResult{
			Error:    fmt.Sprintf("Unsupported database driver: %s", *config.SqlSettings.DriverName),
			ConnType: "raw",
		})
		return
	}

	if err != nil {
		p.API.LogError("Failed to connect to database directly", "error", err)
		respondWithJSON(w, http.StatusInternalServerError, TestResult{
			Error:    fmt.Sprintf("Failed to connect to database: %v", err),
			ConnType: "raw",
		})
		return
	}
	defer db.Close()

	// Run test through helper method
	result, err := p.runDatabaseTest(db, driverName, pageSize)
	if err != nil {
		p.API.LogError("Test failed", "error", err)
		respondWithJSON(w, http.StatusInternalServerError, TestResult{
			Error:    err.Error(),
			ConnType: "raw",
		})
		return
	}

	// Set connection type
	result.ConnType = "raw"

	respondWithJSON(w, http.StatusOK, result)
}

// runDatabaseTest is a helper method that runs the database test with a given DB connection
func (p *Plugin) runDatabaseTest(db *sql.DB, driverName string, batchSize int) (TestResult, error) {
	result := TestResult{}
	const totalRecords = 50000

	p.API.LogInfo("Database driver", "name", driverName)

	// Create test table (no timing metrics)
	var createTableSQL string
	if driverName == "postgres" {
		createTableSQL = `
			CREATE TABLE IF NOT EXISTS plugin_test_rpc (
				id SERIAL PRIMARY KEY,
				data VARCHAR(255) NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)
		`
	} else {
		// MySQL syntax
		createTableSQL = `
			CREATE TABLE IF NOT EXISTS plugin_test_rpc (
				id INT AUTO_INCREMENT PRIMARY KEY,
				data VARCHAR(255) NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)
		`
	}

	_, err := db.Exec(createTableSQL)
	if err != nil {
		return result, fmt.Errorf("failed to create table: %v", err)
	}

	// Check if we need to insert data
	var count int
	countSQL := "SELECT COUNT(*) FROM plugin_test_rpc"
	err = db.QueryRow(countSQL).Scan(&count)
	if err != nil {
		return result, fmt.Errorf("failed to check record count: %v", err)
	}

	// Insert records if needed
	if count < totalRecords {
		p.API.LogInfo(fmt.Sprintf("Inserting records: %d of %d", count, totalRecords))
		startInsert := time.Now()

		// Use transaction for faster inserts
		tx, err := db.Begin()
		if err != nil {
			return result, fmt.Errorf("failed to begin transaction: %v", err)
		}

		var insertStmt *sql.Stmt
		if driverName == "postgres" {
			insertStmt, err = tx.Prepare("INSERT INTO plugin_test_rpc (data) VALUES ($1)")
		} else {
			insertStmt, err = tx.Prepare("INSERT INTO plugin_test_rpc (data) VALUES (?)")
		}

		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				p.API.LogError("Failed to rollback transaction", "error", rbErr)
			}
			return result, fmt.Errorf("failed to prepare statement: %v", err)
		}
		defer insertStmt.Close()

		for i := count; i < totalRecords; i++ {
			_, err = insertStmt.Exec(fmt.Sprintf("Test data %d", i))
			if err != nil {
				if rbErr := tx.Rollback(); rbErr != nil {
					p.API.LogError("Failed to rollback transaction", "error", rbErr)
				}
				return result, fmt.Errorf("failed to insert row %d: %v", i, err)
			}
		}

		err = tx.Commit()
		if err != nil {
			return result, fmt.Errorf("failed to commit transaction: %v", err)
		}

		result.InsertTimeSeconds = time.Since(startInsert).Seconds()
	} else {
		p.API.LogInfo(fmt.Sprintf("Table already has %d or more records", totalRecords))
	}

	// Query the table in batches and measure total time
	startTotalQuery := time.Now()

	// Add page size to result for reference
	result.PageSize = batchSize

	for offset := 0; offset < totalRecords; offset += batchSize {
		var rows *sql.Rows
		var err error

		// Calculate limit - ensure we don't exceed total records
		limit := batchSize
		if offset+batchSize > totalRecords {
			limit = totalRecords - offset
		}

		if driverName == "postgres" {
			rows, err = db.Query("SELECT id, data FROM plugin_test_rpc ORDER BY id LIMIT $1 OFFSET $2", limit, offset)
		} else {
			rows, err = db.Query("SELECT id, data FROM plugin_test_rpc ORDER BY id LIMIT ? OFFSET ?", limit, offset)
		}

		if err != nil {
			return result, fmt.Errorf("failed to query rows at offset %d: %v", offset, err)
		}

		// Read all rows to measure full query time
		for rows.Next() {
			var id int
			var data string
			if err := rows.Scan(&id, &data); err != nil {
				rows.Close()
				return result, fmt.Errorf("failed to scan row: %v", err)
			}
		}
		rows.Close()
	}

	// Calculate total query time
	result.TotalQueryTimeSeconds = time.Since(startTotalQuery).Seconds()
	result.RecordsQueried = totalRecords

	return result, nil
}

func respondWithJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "Failed to encode response"}`))
	}
}
