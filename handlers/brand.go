package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"log"
	"net/http"
)

func GetBrandsHandler(w http.ResponseWriter, r *http.Request) {
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

	rows, err := db.DB.Query("SELECT nama_merek FROM merek")
	if err != nil {
		http.Error(w, "Gagal mengambil data merek", http.StatusInternalServerError)
		log.Printf("❌ Error querying database: %v", err)
		return
	}
	defer rows.Close()

	var brands []string
	for rows.Next() {
		var brand string
		if err := rows.Scan(&brand); err != nil {
			http.Error(w, "Gagal membaca data merek", http.StatusInternalServerError)
			log.Printf("❌ Error scanning row: %v", err)
			return
		}
		brands = append(brands, brand)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Gagal mengambil data merek", http.StatusInternalServerError)
		log.Printf("❌ Error iterating rows: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(brands); err != nil {
		log.Printf("❌ Error encoding response: %v", err)
		http.Error(w, "Gagal mengirim response", http.StatusInternalServerError)
		return
	}
}
