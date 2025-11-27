package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"log"
	"net/http"
)

// Definisikan struct untuk respons JSON agar cocok dengan model di Android
type HistoryItemResponse struct {
	ID               string  `json:"id"`
	Date             string  `json:"tanggal_input"`
	Appliance        string  `json:"nama_perangkat"`
	ApplianceDetails string  `json:"brand"`
	CategoryID       int     `json:"category_id"`
	CategoryName     string  `json:"category_name"`
	HouseCapacity    string  `json:"besar_listrik"`
	Power            float64 `json:"daya"`
	Usage            float64 `json:"durasi"`
	// ================== 1. TAMBAHKAN FIELD INI ==================
	// Nama JSON "dailyEnergy" harus cocok dengan yang diharapkan GSON di Android
	DailyKwh float64 `json:"dailyEnergy"`
	// ==========================================================
}

func GetDeviceHistoryHandler(w http.ResponseWriter, r *http.Request) {
	// Ambil sesi
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	// Cek user ID di sesi
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		log.Println("❌ GetDeviceHistoryHandler: Unauthorized, user_id not found in session")
		http.Error(w, `{"error": "Tidak terautentikasi"}`, http.StatusUnauthorized)
		return
	}

	// ================== 2. QUERY SQL TETAP SAMA ==================
	// Kita tetep ambil daya dan durasi mentah
	query := `
		SELECT 
			rp.id, 
			rp.tanggal_input, 
			rp.nama_perangkat, 
			rp.merek, 
			rp.kategori_id, 
			k.nama_kategori,
			rp.besar_listrik,
			rp.daya, 
			rp.durasi
		FROM riwayat_perangkat rp
		LEFT JOIN kategori k ON rp.kategori_id = k.kategori_id
		WHERE rp.user_id = ?
		ORDER BY rp.tanggal_input DESC, rp.id DESC
	`
	// ====================================================================

	rows, err := db.DB.Query(query, userID)
	if err != nil {
		log.Printf("❌ Error querying device history: %v", err)
		http.Error(w, `{"error": "Gagal mengambil data riwayat"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var historyItems []HistoryItemResponse
	for rows.Next() {
		var item HistoryItemResponse
		// ================== 3. SCAN TETAP SAMA ==================
		if err := rows.Scan(
			&item.ID, &item.Date, &item.Appliance, &item.ApplianceDetails,
			&item.CategoryID, &item.CategoryName, &item.HouseCapacity,
			&item.Power, &item.Usage,
		); err != nil {
			log.Printf("❌ Error scanning history row: %v", err)
			http.Error(w, `{"error": "Gagal membaca data riwayat"}`, http.StatusInternalServerError)
			return
		}
		// ===============================================================

		// ================== 4. HITUNG MANUAL DI SINI! ==================
		// Ini adalah perbaikan utamanya. Kita hitung manual di backend.
		item.DailyKwh = (item.Power * item.Usage) / 1000.0
		// ===============================================================

		historyItems = append(historyItems, item)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ Error iterating history rows: %v", err)
		http.Error(w, `{"error": "Gagal mengambil data riwayat"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(historyItems)
}

