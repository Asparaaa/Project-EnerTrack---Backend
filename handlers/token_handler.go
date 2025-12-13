package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"EnerTrack-BE/db"
)

type TokenRequest struct {
	UserID   int    `json:"user_id"`
	FCMToken string `json:"fcm_token"`
}

// Helper untuk mengirim error sebagai JSON
func sendErrorJSON(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	response := map[string]string{"message": message, "status": "error"}
	// Menambahkan log di sisi server agar kita tahu persis error apa yang dikirim
	log.Printf("⚠️ Responding with %d Error: %s", status, message)
	json.NewEncoder(w).Encode(response)
}

// Handler untuk menyimpan/update token FCM dari Android ke Database
func UpdateFcmTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Menggunakan helper untuk respons JSON yang konsisten
		sendErrorJSON(w, "Method not allowed. Only POST is supported.", http.StatusMethodNotAllowed)
		return
	}

	var req TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("❌ JSON Decode Error: %v", err)
		sendErrorJSON(w, "Invalid JSON payload.", http.StatusBadRequest)
		return
	}

	// Validasi simpel
	if req.UserID == 0 || req.FCMToken == "" {
		sendErrorJSON(w, "User ID and Token are required in the payload.", http.StatusBadRequest)
		return
	}

	// Update token di database
	query := "UPDATE users SET fcm_token = ? WHERE user_id = ?"
	_, err := db.DB.Exec(query, req.FCMToken, req.UserID)
	if err != nil {
		log.Printf("❌ Gagal update token user %d: %v", req.UserID, err)
		sendErrorJSON(w, "Database error during token update.", http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Token FCM updated untuk User ID: %d", req.UserID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "message":"Token updated"}`))
}