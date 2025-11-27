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

	// --- CCTV DEBUGGING (Kita pasang di sini) ---
	log.Println("============================================")
	log.Println("DEBUG: SEDANG MENGECEK VARIABLE DATABASE...")
	log.Printf("DEBUG: User: '%s'", dbUser)
	log.Printf("DEBUG: Host: '%s'", dbHost)
	log.Printf("DEBUG: Port: '%s'", dbPort)
	log.Println("============================================")
	// ------------------------------------------

	// 2. Logic Fallback: Kalau Host KOSONG, baru pake settingan Localhost (Laptop)
	if dbHost == "" {
		log.Println("⚠️ WARNING: Variable Railway KOSONG atau Tidak Terbaca.")
		log.Println("⚠️ Mengalihkan koneksi ke LOCALHOST (Laptop)...")
		
		dbUser = "root"
		dbPass = "password123"
		dbHost = "127.0.0.1"
		dbPort = "3306"
		dbName = "elektronik_rumah"
	}

	// 3. Gabungin jadi format DSN (Data Source Name)
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

	// --- Logic Create Table & Data (Tidak diubah) ---

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


	createUsersTableSQL := `
		CREATE TABLE IF NOT EXISTS users (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(100) NOT NULL,
			email VARCHAR(100) NOT NULL UNIQUE,
			password VARCHAR(255) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err = DB.Exec(createUsersTableSQL)
	if err != nil {
		log.Printf("❌ Warning: Gagal membuat tabel users: %v", err)
	} else {
		log.Println("✅ Tabel 'users' siap digunakan/sudah ada.")
	}

	var count int
	err = DB.QueryRow("SELECT COUNT(*) FROM merek").Scan(&count)
	if err != nil {
		log.Printf("Warning: Could not check merek table count: %v", err)
	} else {
		log.Printf("Jumlah data merek saat ini: %d", count)
	}

	log.Println("✅ Database berhasil terkoneksi")
}