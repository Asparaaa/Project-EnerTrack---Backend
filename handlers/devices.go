package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"log"
	"net/http"
)

type DeviceResponse struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	PowerWatt float64 `json:"power_watt"`
	CategoryID int    `json:"category_id"`
}

func GetDevicesByBrandHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		return
	}

	// Ambil parameter brand dari query string
	brand := r.URL.Query().Get("brand")
	if brand == "" {
		http.Error(w, `{"error": "Parameter brand diperlukan"}`, http.StatusBadRequest)
		return
	}

	// Query database: ambil devices berdasarkan brand
	rows, err := db.DB.Query(`
		SELECT p.id, p.nama_produk, p.daya_watt, p.kategori_id
		FROM produk p
		JOIN merek m ON p.merek_id = m.id
		WHERE m.nama_merek = ?
		ORDER BY p.nama_produk
	`, brand)
	
	if err != nil {
		log.Printf("❌ Error querying devices for brand %s: %v", brand, err)
		http.Error(w, `{"error": "Gagal mengambil data devices"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var devices []DeviceResponse
	for rows.Next() {
		var device DeviceResponse
		err := rows.Scan(&device.ID, &device.Name, &device.PowerWatt, &device.CategoryID)
		if err != nil {
			log.Printf("❌ Error scanning device row: %v", err)
			continue
		}
		devices = append(devices, device)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ Error iterating device rows: %v", err)
		http.Error(w, `{"error": "Gagal memproses data devices"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devices)
}