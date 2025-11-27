package db

import (
	"database/sql"
	"log"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

func InitDB() {
	var err error
	DB, err = sql.Open("mysql", "root:password123@tcp(127.0.0.1:3306)/elektronik_rumah")
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	if err := DB.Ping(); err != nil {
		log.Fatalf("Database tidak bisa dijangkau: %v", err)
	}

	// Create merek table if not exists
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS merek (
			id INT AUTO_INCREMENT PRIMARY KEY,
			nama_merek VARCHAR(100) NOT NULL UNIQUE
		)
	`
	_, err = DB.Exec(createTableSQL)
	if err != nil {
		log.Printf("Warning: Could not create merek table: %v", err)
	}

	// Insert default brands if table is empty
	var count int
	err = DB.QueryRow("SELECT COUNT(*) FROM merek").Scan(&count)
	if err != nil {
		log.Printf("Warning: Could not check merek table count: %v", err)
	}

	log.Println("✅ Database berhasil terkoneksi")
}
