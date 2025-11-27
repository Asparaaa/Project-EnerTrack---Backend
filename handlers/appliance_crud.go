// appliances_crud.go - Complete CRUD handlers untuk appliances
package handlers

import (
	"EnerTrack-BE/db"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Struct untuk input data appliance
type ApplianceInput struct {
	Name         string `json:"name"`
	Brand        string `json:"brand"`
	CategoryID   int    `json:"category_id"`
	PowerRating  int    `json:"power_rating"`
	DailyUsage   int    `json:"daily_usage"`
	Quantity     int    `json:"quantity"`
	BesarListrik string `json:"besar_listrik"`
}

// Struct untuk update appliance
type ApplianceUpdate struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Brand        string `json:"brand"`
	CategoryID   int    `json:"category_id"`
	PowerRating  int    `json:"power_rating"`
	DailyUsage   int    `json:"daily_usage"`
	Quantity     int    `json:"quantity"`
	BesarListrik string `json:"besar_listrik"`
}

func getTariffByCapacity(capacity string) float64 {
	valStr := strings.Replace(strings.Split(capacity, " ")[0], ".", "", -1)
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return 1444.70
	}

	if val <= 900 {
		return 1352.0
	}
	if val <= 2200 {
		return 1444.70
	}
	// 3.500 VA ke atas
	return 1699.53
}

// Create
func CreateApplianceHandler(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		return
	}

	// Ambil session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		log.Printf("❌ Error mendapatkan session: %v", err)
		http.Error(w, "Sesi tidak ditemukan", http.StatusUnauthorized)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, "User ID tidak ditemukan", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var input ApplianceInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "Data tidak valid", http.StatusBadRequest)
		return
	}

	// Validasi input (termasuk BesarListrik)
	if input.Name == "" || input.PowerRating <= 0 || input.DailyUsage <= 0 || input.BesarListrik == "" {
		http.Error(w, "Data tidak lengkap (termasuk besar listrik)", http.StatusBadRequest)
		return
	}

	if input.Quantity <= 0 {
		input.Quantity = 1 // Default quantity
	}

	// Generate ID submit baru atau ambil yang sudah ada
	var idSubmit string
	err = db.DB.QueryRow(`
		SELECT id_submit 
		FROM riwayat_perangkat 
		WHERE user_id = ? 
		ORDER BY tanggal_input DESC 
		LIMIT 1`, userID).Scan(&idSubmit)

	if err != nil {
		idSubmit = fmt.Sprintf("SUBMIT_%d_%d", userID, time.Now().Unix())
	}

	dailyEnergyKWh := float64(input.PowerRating*input.DailyUsage*input.Quantity) / 1000.0
	weeklyUsage := dailyEnergyKWh * 7
	monthlyUsage := dailyEnergyKWh * 30

	tarifPerKWh := getTariffByCapacity(input.BesarListrik)
	monthlyCost := monthlyUsage * tarifPerKWh
	query := `
		INSERT INTO riwayat_perangkat 
		(user_id, id_submit, nama_perangkat, merek, kategori_id, daya, durasi, 
		 besar_listrik, Weekly_Usage, Monthly_Usage, Monthly_cost, tanggal_input) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())`

	result, err := db.DB.Exec(query, userID, idSubmit, input.Name, input.Brand,
		input.CategoryID, input.PowerRating, input.DailyUsage,
		input.BesarListrik, weeklyUsage, monthlyUsage, monthlyCost)

	if err != nil {
		log.Printf("❌ Error inserting appliance: %v", err)
		http.Error(w, "Gagal menyimpan perangkat", http.StatusInternalServerError)
		return
	}

	// Get inserted ID
	insertedID, err := result.LastInsertId()
	if err != nil {
		log.Printf("❌ Error getting inserted ID: %v", err)
	}

	// Response
	response := map[string]interface{}{
		"message": "Perangkat berhasil ditambahkan",
		"id":      insertedID,
		"data": map[string]interface{}{
			"id":            insertedID,
			"name":          input.Name,
			"brand":         input.Brand,
			"category_id":   input.CategoryID,
			"power_rating":  input.PowerRating,
			"daily_usage":   input.DailyUsage,
			"quantity":      input.Quantity,
			"besar_listrik": input.BesarListrik,
			"daily_energy":  dailyEnergyKWh,
			"monthly_usage": monthlyUsage,
			"monthly_cost":  monthlyCost,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// READ
func GetUserAppliancesHandler(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
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

	// Ambil session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		log.Printf("❌ Error mendapatkan session: %v", err)
		http.Error(w, "Sesi tidak ditemukan", http.StatusUnauthorized)
		return
	}

	// Cek user_id di session
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		log.Println("❌ User ID tidak ditemukan di sesi")
		http.Error(w, "User ID tidak ditemukan", http.StatusUnauthorized)
		return
	}

	// Ambil appliances dari id_submit terakhir
	var idSubmit string
	err = db.DB.QueryRow(`
		SELECT id_submit 
		FROM riwayat_perangkat 
		WHERE user_id = ? 
		ORDER BY tanggal_input DESC, id DESC 
		LIMIT 1`, userID).Scan(&idSubmit)

	if err != nil {
		// Jika belum ada data, return empty array
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	// Ambil semua appliances dengan id_submit tersebut, termasuk kategori dan besar_listrik
	rows, err := db.DB.Query(`
		SELECT rp.id, rp.nama_perangkat, rp.merek, rp.daya, rp.durasi, 
			   rp.Weekly_Usage, rp.Monthly_Usage, rp.Monthly_cost,
			   COALESCE(rp.besar_listrik, '') as besar_listrik,
			   COALESCE(k.nama_kategori, 'Others') as kategori
		FROM riwayat_perangkat rp
		LEFT JOIN kategori k ON rp.kategori_id = k.kategori_id
		WHERE rp.user_id = ? AND rp.id_submit = ?`, userID, idSubmit)

	if err != nil {
		log.Printf("❌ Gagal query database: %v", err)
		http.Error(w, "Gagal mengambil data perangkat", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var appliances []map[string]interface{}
	for rows.Next() {
		var id int
		var name, brand, category, besarListrik string
		var power, duration int
		var weeklyUsage, monthlyUsage, monthlyCost float64

		if err := rows.Scan(&id, &name, &brand, &power, &duration, &weeklyUsage, &monthlyUsage, &monthlyCost, &besarListrik, &category); err != nil {
			log.Printf("❌ Error scanning row: %v", err)
			continue
		}

		// Calculate daily energy for frontend compatibility
		dailyEnergy := float64(power*duration) / 1000.0 // kWh per day

		appliance := map[string]interface{}{
			"id":            id,
			"name":          name,
			"brand":         brand,
			"category":      category,
			"powerRating":   power,
			"dailyUsage":    duration,
			"quantity":      1,
			"besarListrik":  besarListrik,
			"dailyEnergy":   dailyEnergy,
			"monthlyEnergy": monthlyUsage,
		}
		appliances = append(appliances, appliance)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(appliances); err != nil {
		log.Printf("❌ Error encoding response: %v", err)
		http.Error(w, "Gagal mengirim response", http.StatusInternalServerError)
	}
}

// UPDATE - Edit appliance yang sudah ada
func UpdateApplianceHandler(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPut {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		return
	}

	// Ambil session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		http.Error(w, "Sesi tidak ditemukan", http.StatusUnauthorized)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, "User ID tidak ditemukan", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var input ApplianceUpdate
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "Data tidak valid", http.StatusBadRequest)
		return
	}

	// Validasi input (termasuk BesarListrik)
	if input.ID <= 0 || input.Name == "" || input.PowerRating <= 0 || input.DailyUsage <= 0 || input.BesarListrik == "" {
		http.Error(w, "Data tidak lengkap atau tidak valid", http.StatusBadRequest)
		return
	}

	if input.Quantity <= 0 {
		input.Quantity = 1
	}

	// Cek apakah appliance milik user ini
	var existingUserID int
	err = db.DB.QueryRow("SELECT user_id FROM riwayat_perangkat WHERE id = ?", input.ID).Scan(&existingUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Perangkat tidak ditemukan", http.StatusNotFound)
			return
		}
		log.Printf("❌ Error checking appliance ownership: %v", err)
		http.Error(w, "Error mengakses data", http.StatusInternalServerError)
		return
	}

	if existingUserID != userID {
		http.Error(w, "Tidak memiliki akses ke perangkat ini", http.StatusForbidden)
		return
	}

	dailyEnergyKWh := float64(input.PowerRating*input.DailyUsage*input.Quantity) / 1000.0
	weeklyUsage := dailyEnergyKWh * 7
	monthlyUsage := dailyEnergyKWh * 30

	tarifPerKWh := getTariffByCapacity(input.BesarListrik)
	monthlyCost := monthlyUsage * tarifPerKWh
	query := `
		UPDATE riwayat_perangkat 
		SET nama_perangkat = ?, merek = ?, kategori_id = ?, daya = ?, durasi = ?,
			besar_listrik = ?, Weekly_Usage = ?, Monthly_Usage = ?, Monthly_cost = ?
		WHERE id = ? AND user_id = ?`

	result, err := db.DB.Exec(query, input.Name, input.Brand, input.CategoryID,
		input.PowerRating, input.DailyUsage, input.BesarListrik, weeklyUsage, monthlyUsage, monthlyCost,
		input.ID, userID)

	if err != nil {
		log.Printf("❌ Error updating appliance: %v", err)
		http.Error(w, "Gagal mengupdate perangkat", http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Tidak ada perubahan data", http.StatusNotModified)
		return
	}

	// Response
	response := map[string]interface{}{
		"message": "Perangkat berhasil diupdate",
		"data": map[string]interface{}{
			"id":            input.ID,
			"name":          input.Name,
			"brand":         input.Brand,
			"category_id":   input.CategoryID,
			"power_rating":  input.PowerRating,
			"daily_usage":   input.DailyUsage,
			"quantity":      input.Quantity,
			"besar_listrik": input.BesarListrik,
			"daily_energy":  dailyEnergyKWh,
			"monthly_usage": monthlyUsage,
			"monthly_cost":  monthlyCost,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// DELETE - Hapus appliance
func DeleteApplianceHandler(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodDelete {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		return
	}

	// Ambil session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		http.Error(w, "Sesi tidak ditemukan", http.StatusUnauthorized)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, "User ID tidak ditemukan", http.StatusUnauthorized)
		return
	}

	// Parse request body untuk mendapatkan appliance ID
	var requestData struct {
		ID int `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Data tidak valid", http.StatusBadRequest)
		return
	}

	result, err := db.DB.Exec(`
		DELETE FROM riwayat_perangkat 
		WHERE id = ? AND user_id = ?`, requestData.ID, userID)

	if err != nil {
		log.Printf("❌ Gagal menghapus appliance: %v", err)
		http.Error(w, "Gagal menghapus perangkat", http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "Perangkat tidak ditemukan", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Perangkat berhasil dihapus",
	})
}

func GetApplianceByIDHandler(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
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

	// Ambil session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		http.Error(w, "Sesi tidak ditemukan", http.StatusUnauthorized)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, "User ID tidak ditemukan", http.StatusUnauthorized)
		return
	}

	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 3 {
		http.Error(w, "ID perangkat tidak ditemukan di URL", http.StatusBadRequest)
		return
	}

	applianceID, err := strconv.Atoi(pathParts[len(pathParts)-1])
	if err != nil {
		http.Error(w, "ID perangkat tidak valid", http.StatusBadRequest)
		return
	}

	var id int
	var name, brand, category, besarListrik string
	var power, duration int
	var weeklyUsage, monthlyUsage, monthlyCost float64

	query := `
		SELECT rp.id, rp.nama_perangkat, rp.merek, rp.daya, rp.durasi, 
			   rp.Weekly_Usage, rp.Monthly_Usage, rp.Monthly_cost,
			   COALESCE(rp.besar_listrik, '') as besar_listrik,
			   COALESCE(k.nama_kategori, 'Others') as kategori
		FROM riwayat_perangkat rp
		LEFT JOIN kategori k ON rp.kategori_id = k.id
		WHERE rp.id = ? AND rp.user_id = ?`

	err = db.DB.QueryRow(query, applianceID, userID).Scan(
		&id, &name, &brand, &power, &duration,
		&weeklyUsage, &monthlyUsage, &monthlyCost, &besarListrik, &category)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Perangkat tidak ditemukan", http.StatusNotFound)
			return
		}
		log.Printf("❌ Error querying appliance: %v", err)
		http.Error(w, "Error mengambil data perangkat", http.StatusInternalServerError)
		return
	}

	// Calculate daily energy
	dailyEnergy := float64(power*duration) / 1000.0

	appliance := map[string]interface{}{
		"id":            id,
		"name":          name,
		"brand":         brand,
		"category":      category,
		"powerRating":   power,
		"dailyUsage":    duration,
		"quantity":      1,
		"besarListrik":  besarListrik,
		"dailyEnergy":   dailyEnergy,
		"monthlyEnergy": monthlyUsage,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(appliance)
}
