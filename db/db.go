package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

func InitDB() {
	// 1. Coba ambil settingan dari Railway (Environment Variables)
	dbUser := os.Getenv("MYSQLUSER")
	dbPass := os.Getenv("MYSQLPASSWORD")
	dbHost := os.Getenv("MYSQLHOST")
	dbPort := os.Getenv("MYSQLPORT")
	dbName := os.Getenv("MYSQLDATABASE")

	// 2. Kalau kosong, berarti lagi jalan di Laptop (Localhost)
	// Ini settingan sesuai code lama kamu
	if dbHost == "" {
		dbUser = "root"
		dbPass = "password123"
		dbHost = "127.0.0.1"
		dbPort = "3306"
		dbName = "elektronik_rumah"
	}

	// 3. Gabungin jadi format DSN (Data Source Name)
	// Format: user:pass@tcp(host:port)/dbname?parseTime=true
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", 
		dbUser, dbPass, dbHost, dbPort, dbName)

	var err error
	DB, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	if err := DB.Ping(); err != nil {
		log.Fatalf("Database tidak bisa dijangkau: %v", err)
	}

	// --- Logic Create Table Kamu Tetap Aman di Bawah Ini ---

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
	// Cek tabel ada isinya atau nggak
	err = DB.QueryRow("SELECT COUNT(*) FROM merek").Scan(&count)
	if err != nil {
		// Kalau error (misal tabel baru dibuat), anggap aja kosong/warning
		log.Printf("Warning: Could not check merek table count: %v", err)
	} else {
		log.Printf("Jumlah data merek saat ini: %d", count)
	}

	log.Println("✅ Database berhasil terkoneksi")
}