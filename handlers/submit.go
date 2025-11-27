package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"EnerTrack-BE/db"

	"github.com/google/uuid"
)

// Device struct untuk menerima data dari frontend
type DeviceInput struct {
	Jenis_Pembayaran string  `json:"jenis_pembayaran"`
	Besar_Listrik    string  `json:"besar_listrik"`
	Name             string  `json:"name"`
	Brand            string  `json:"brand"`
	Power            float64 `json:"power"`
	Duration         float64 `json:"duration"`
	CategoryID       *int    `json:"category_id"`
}

// getCurrentDate mengembalikan tanggal saat ini dalam format YYYY-MM-DD
func getCurrentDate() string {
	return time.Now().Format("2006-01-02")
}

// SubmitHandler menangani submit data perangkat dengan id_submit sama untuk semua device dalam satu request
func SubmitHandler(w http.ResponseWriter, r *http.Request) {

	// Ambil session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		log.Printf("❌ Error mendapatkan session: %v", err)
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	// Debug: Log all session values
	log.Printf("🔍 Session values: %+v", session.Values)

	// Ambil username dari session
	email, ok := session.Values["email"].(string)
	if !ok {
		log.Printf("❌ Email tidak ditemukan di sesi. Session values: %+v", session.Values)
		http.Error(w, `{"error": "Email tidak ditemukan"}`, http.StatusBadRequest)
		return
	}

	// Ambil user_id dari database
	var userID int
	err = db.DB.QueryRow("SELECT user_id FROM users WHERE email = ?", email).Scan(&userID)
	if err != nil {
		log.Printf("❌ Gagal mendapatkan user_id: %v", err)
		http.Error(w, `{"error": "Gagal mengambil user ID"}`, http.StatusInternalServerError)
		return
	}
	log.Printf("✅ email ditemukan: %s (ID: %d)", email, userID)

	// Validasi metode HTTP
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "Metode tidak diizinkan"}`, http.StatusMethodNotAllowed)
		return
	}

	// Baca payload JSON dengan struct yang sudah diperbaiki
	var inputData struct {
		BillingType string `json:"billingtype"`
		Electricity struct {
			Amount float64 `json:"amount,omitempty"`
			Kwh    float64 `json:"kwh,omitempty"`
		} `json:"electricity"`
		Devices []DeviceInput `json:"devices"`
	}

	if err := json.NewDecoder(r.Body).Decode(&inputData); err != nil {
		log.Printf("❌ Error decoding JSON: %v", err)
		http.Error(w, `{"error": "Gagal membaca data JSON"}`, http.StatusBadRequest)
		return
	}
	log.Printf("✅ Data diterima dari %s: %+v", email, inputData)

	// Validasi devices
	if len(inputData.Devices) == 0 {
		http.Error(w, `{"error": "Data perangkat tidak boleh kosong"}`, http.StatusBadRequest)
		return
	}

	// Generate id_submit (misalnya menggunakan UUID)
	idSubmit := uuid.New().String()
	log.Printf("✅ id_submit dibuat: %s", idSubmit)

	// Gunakan transaksi untuk menyimpan seluruh device dengan id_submit yang sama
	tanggal := getCurrentDate()
	tx, err := db.DB.Begin()
	if err != nil {
		log.Printf("❌ Gagal memulai transaksi: %v", err)
		http.Error(w, `{"error": "Gagal memulai simpan data"}`, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback() // rollback jika gagal

	// Simpan setiap device dengan id_submit yang sama
	for _, device := range inputData.Devices {
		if device.Jenis_Pembayaran == "" || device.Besar_Listrik == "" || device.Name == "" || device.Brand == "" || device.Power <= 0 || device.Duration <= 0 {
			log.Println("❌ Data perangkat tidak valid:", device)
			http.Error(w, `{"error": "Nama, merek, daya, dan durasi harus diisi dan lebih besar dari 0"}`, http.StatusBadRequest)
			return
		}

		// Hitung weekly dan monthly usage
		weeklyUsage := (device.Power * device.Duration * 7) / 1000.0 // kWh per minggu
		monthlyUsage := float64(weeklyUsage * 4)                     // kWh per bulan
		tariffRate := getTariffRate(device.Besar_Listrik)
		monthlyCost := monthlyUsage * tariffRate

		// Handle category_id yang bisa null
		var categoryID interface{}
		if device.CategoryID != nil {
			categoryID = *device.CategoryID
		} else {
			categoryID = nil // Akan menjadi NULL di database
		}

		_, err := tx.Exec(`
            INSERT INTO riwayat_perangkat 
            (id_submit, user_id, Jenis_Pembayaran, Besar_Listrik, nama_perangkat, merek, daya, durasi, Weekly_Usage, Monthly_Usage, Monthly_cost, tanggal_input, kategori_id) 
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			idSubmit, userID, device.Jenis_Pembayaran, device.Besar_Listrik, device.Name, device.Brand, device.Power, device.Duration, weeklyUsage, monthlyUsage, monthlyCost, tanggal, categoryID,
		)

		if err != nil {
			log.Printf("❌ Gagal menyimpan perangkat: %v", err)
			http.Error(w, `{"error": "Gagal menyimpan data perangkat"}`, http.StatusInternalServerError)
			return
		}

		// ✅ Debug log untuk memastikan CategoryID diterima
		log.Printf("✅ Device saved with CategoryID: %v", categoryID)
	}

	// Commit transaksi
	if err := tx.Commit(); err != nil {
		log.Printf("❌ Gagal commit transaksi: %v", err)
		http.Error(w, `{"error": "Gagal menyimpan data"}`, http.StatusInternalServerError)
		return
	}

	// Kirim respons sukses
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":     "Data berhasil disimpan",
		"id_submit":   idSubmit,
		"total_items": len(inputData.Devices),
	})
}
