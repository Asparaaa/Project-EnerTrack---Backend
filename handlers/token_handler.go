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

// Handler untuk menyimpan/update token FCM dari Android ke Database
func UpdateFcmTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validasi simpel
	if req.UserID == 0 || req.FCMToken == "" {
		http.Error(w, "User ID and Token required", http.StatusBadRequest)
		return
	}

	// Update token di database
	query := "UPDATE users SET fcm_token = ? WHERE user_id = ?"
	_, err := db.DB.Exec(query, req.FCMToken, req.UserID)
	if err != nil {
		log.Printf("❌ Gagal update token user %d: %v", req.UserID, err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Token FCM updated untuk User ID: %d", req.UserID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "message":"Token updated"}`))
}