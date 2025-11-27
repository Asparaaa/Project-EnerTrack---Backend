package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"EnerTrack-BE/db"
	// Pastikan diimpor jika handler ini juga dilindungi sesi
)	

type CategoryResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func GetCategoriesHandler(w http.ResponseWriter, r *http.Request) {
	db.InitDB()

	// Pemeriksaan sesi (jika dilindungi, seperti yang kita diskusikan)
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		log.Printf("❌ Error getting session in GetCategoriesHandler: %v", err)
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	_, ok := session.Values["username"].(string)
	if !ok {
		log.Println("❌ Unauthorized access to GetCategoriesHandler: username not found in session")
		http.Error(w, `{"error": "Tidak terautentikasi"}`, http.StatusUnauthorized)
		return
	}
	// Akhir pemeriksaan sesi

	if r.Method != http.MethodGet {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.DB.Query("SELECT kategori_id, nama_kategori FROM kategori")
	if err != nil {
		log.Printf("❌ Error querying kategori: %v", err)
		http.Error(w, `{"error": "Gagal mengambil data kategori"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var categories []CategoryResponse
	for rows.Next() {
		var cat CategoryResponse
		// ✅ Scan ke kategori_id dan nama_kategori
		if err := rows.Scan(&cat.ID, &cat.Name); err != nil {
			log.Printf("❌ Error scanning kategori row: %v", err)
			http.Error(w, `{"error": "Gagal membaca data kategori"}`, http.StatusInternalServerError)
			return
		}
		categories = append(categories, cat)
	}

	if err := rows.Err(); err != nil {
		log.Printf("❌ Error iterating over kategori rows: %v", err)
		http.Error(w, `{"error": "Gagal mengambil data kategori"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(categories)
}
