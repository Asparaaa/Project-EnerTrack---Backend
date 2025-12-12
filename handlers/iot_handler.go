package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"EnerTrack-BE/db" // Import package database kamu

	"cloud.google.com/go/firestore" // Import library firestore
)

// IotData adalah struktur JSON yang wajib dikirim oleh Arduino
type IotData struct {
	UserID      int     `json:"user_id"`      // ID User pemilik alat (Wajib)
	DeviceLabel string  `json:"device_label"` // Nama alat, misal: "Main Smart Meter" (Wajib)
	Voltase     float64 `json:"voltase"`      // Tegangan (V)
	Ampere      float64 `json:"ampere"`       // Arus (A)
	Watt        float64 `json:"watt"`         // Daya (W)
	KwhTotal    float64 `json:"kwh_total"`    // Total kWh (opsional, kirim 0 jika belum ada)
}

// IotInputHandler menangani request POST dari Arduino
// Menerima parameter tambahan fs (Firestore Client)
func IotInputHandler(w http.ResponseWriter, r *http.Request, fs *firestore.Client) {
	// 1. Cek Method (Harus POST)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
		return
	}

	// 2. Decode JSON dari Body Request
	var data IotData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("❌ Error decode JSON IoT: %v", err)
		http.Error(w, "Format JSON tidak valid", http.StatusBadRequest)
		return
	}

	// 3. Validasi Data Sederhana
	if data.UserID == 0 || data.DeviceLabel == "" {
		http.Error(w, "user_id dan device_label wajib diisi", http.StatusBadRequest)
		return
	}

	// --- A. SIMPAN KE MYSQL (Arsip History) ---
	// Menggunakan db.DB yang sudah di-init di main.go
	query := `
        INSERT INTO energy_logs (user_id, device_label, voltase, ampere, watt, kwh_total) 
        VALUES (?, ?, ?, ?, ?, ?)
    `
	// Eksekusi query insert
	_, err := db.DB.Exec(query, data.UserID, data.DeviceLabel, data.Voltase, data.Ampere, data.Watt, data.KwhTotal)

	if err != nil {
		log.Printf("❌ Gagal simpan ke MySQL: %v", err)
		// Kita tidak return error di sini supaya proses update Firebase tetap jalan
		// (Fallback logic: kalau DB mati, setidaknya live monitoring jalan)
	} else {
		log.Printf("✅ Data tersimpan di MySQL (User: %d, Alat: %s)", data.UserID, data.DeviceLabel)
	}

	// --- B. UPDATE KE FIREBASE (Live Monitoring) ---
	if fs != nil {
		ctx := context.Background()

		// Membuat Document ID yang unik: "user{ID}_{NamaAlat}"
		// Contoh: "user11_Main Smart Meter"
		// Tujuannya: Agar data lama tertimpa data baru (Live Update), bukan membuat baris baru.
		docID := fmt.Sprintf("user%d_%s", data.UserID, data.DeviceLabel)

		// Set data ke dokumen tersebut
		_, err = fs.Collection("monitoring_live").Doc(docID).Set(ctx, map[string]interface{}{
			"user_id":     data.UserID,
			"device_name": data.DeviceLabel,      // Nama ini yang akan muncul di UI Android
			"voltase":     data.Voltase,
			"ampere":      data.Ampere,
			"watt":        data.Watt,
			"kwh_total":   data.KwhTotal,
			"status":      "ON",                      // Status penanda alat aktif
			"last_update": firestore.ServerTimestamp, // Waktu server Google
		})

		if err != nil {
			log.Printf("❌ Gagal update Firebase: %v", err)
		} else {
			log.Printf("✅ Data terupdate di Firebase Doc: %s", docID)
		}
	} else {
		log.Println("⚠️ Firestore Client bernilai nil (belum connect), skip update Firebase.")
	}

	// 4. Kirim Response Sukses ke Arduino
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "message":"Data IoT received and processed"}`))
}