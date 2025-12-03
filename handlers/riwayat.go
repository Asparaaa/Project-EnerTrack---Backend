package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"fmt"
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
	// Nama JSON "dailyEnergy" harus cocok dengan yang diharapkan GSON di Android
	DailyKwh float64 `json:"dailyEnergy"`
}

// Struct khusus untuk dropdown chat
type DeviceOption struct {
	Label   string `json:"label"`   // Nama untuk ditampilkan (misal: "AC Kamar")
	Context string `json:"context"` // String lengkap untuk AI (misal: "AC Kamar (Samsung), 400 Watt...")
}

// GetDeviceHistoryHandler mengambil seluruh riwayat perangkat user
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
		if err := rows.Scan(
			&item.ID, &item.Date, &item.Appliance, &item.ApplianceDetails,
			&item.CategoryID, &item.CategoryName, &item.HouseCapacity,
			&item.Power, &item.Usage,
		); err != nil {
			log.Printf("❌ Error scanning history row: %v", err)
			http.Error(w, `{"error": "Gagal membaca data riwayat"}`, http.StatusInternalServerError)
			return
		}

		// Hitung manual daily kWh
		item.DailyKwh = (item.Power * item.Usage) / 1000.0

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

// GetUniqueDevicesHandler mengambil daftar perangkat unik (untuk Dropdown Chat)
func GetUniqueDevicesHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Validasi Session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, `{"error": "Tidak terautentikasi"}`, http.StatusUnauthorized)
		return
	}

	// 2. Query ambil nama, merek, daya, durasi dari riwayat
	// Kita urutkan ID DESC biar dapet data settingan terakhir user untuk alat tersebut
	query := `
		SELECT nama_perangkat, merek, daya, durasi 
		FROM riwayat_perangkat 
		WHERE user_id = ? 
		ORDER BY id DESC
	`

	rows, err := db.DB.Query(query, userID)
	if err != nil {
		log.Printf("❌ Error query unique devices: %v", err)
		http.Error(w, `{"error": "Gagal query database"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// 3. Filter Duplikat (Manual di Go)
	// Gunakan Map untuk mengecek apakah nama perangkat sudah pernah dimasukkan
	uniqueMap := make(map[string]bool)
	var options []DeviceOption

	// Tambahkan Opsi Default paling atas
	options = append(options, DeviceOption{
		Label:   "Pilih Perangkat (Umum)",
		Context: "", // Context kosong berarti pertanyaan umum
	})

	for rows.Next() {
		var nama, merek string
		var daya, durasi float64

		if err := rows.Scan(&nama, &merek, &daya, &durasi); err != nil {
			continue
		}

		// Kalau nama perangkat ini belum ada di map, berarti ini data terbaru (karena ORDER BY DESC)
		if !uniqueMap[nama] {
			uniqueMap[nama] = true

			// Format Context String: Ini data rahasia yang bakal dikirim ke AI
			// Contoh output: "AC Kamar (Samsung), Daya 400 Watt, Nyala 8.0 Jam/hari"
			contextStr := fmt.Sprintf("%s (%s), Daya %.0f Watt, Nyala %.1f Jam/hari",
				nama, merek, daya, durasi)

			options = append(options, DeviceOption{
				Label:   nama,      // Ini yang muncul di Layar HP User
				Context: contextStr, // Ini yang dikirim ke Backend Chat
			})
		}
	}

	// 4. Kirim Response JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(options)
}