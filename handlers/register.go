package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"log"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

type RegistrationRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RegisterHandler
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error": "Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req RegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Invalid request format"}`, http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Password == "" || req.Username == "" {
		http.Error(w, `{"error": "Username, email, and password are required"}`, http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("❌ Gagal membuat hash password: %v", err)
		http.Error(w, `{"error": "Gagal memproses registrasi"}`, http.StatusInternalServerError)
		return
	}

	_, err = db.DB.Exec("INSERT INTO users (username, email, password) VALUES (?, ?, ?)",
		req.Username, req.Email, string(hashedPassword))

	if err != nil {
		log.Printf("❌ Error saat insert data registrasi: %v", err)
		http.Error(w, `{"error": "Gagal registrasi. Email atau username mungkin sudah digunakan."}`, http.StatusInternalServerError)
		return
	}

	log.Printf("✅ User registered successfully with hashed password: %s", req.Username)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Registrasi berhasil"})
}
