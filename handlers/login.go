package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"log"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// LoginHandler
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		return
	}

	var creds struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}

	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, `{"error": "Data tidak valid"}`, http.StatusBadRequest)
		return
	}

	var storedHashedPassword, username string
	var userID int

	query := "SELECT user_id, username, password FROM users WHERE email = ?"
	err := db.DB.QueryRow(query, creds.Email).Scan(&userID, &username, &storedHashedPassword)

	if err != nil {
		log.Printf("Login attempt failed for email %s: %v", creds.Email, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email atau password salah"})
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(storedHashedPassword), []byte(creds.Password))
	if err != nil {
		log.Printf("Invalid password for user %s. Bcrypt comparison failed: %v", username, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email atau password salah"})
		return
	}

	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		log.Printf("❌ LoginHandler: Error mendapatkan sesi: %v", err)
		http.Error(w, `{"error": "Gagal memulai sesi"}`, http.StatusInternalServerError)
		return
	}

	session.Values["user_id"] = userID
	session.Values["email"] = creds.Email
	session.Values["username"] = username

	if creds.Remember {
		session.Options.MaxAge = 30 * 24 * 60 * 60
	}

	if err = session.Save(r, w); err != nil {
		log.Printf("❌ LoginHandler: Error menyimpan sesi: %v", err)
		http.Error(w, `{"error": "Gagal menyimpan sesi"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Login successful for user: %s (ID: %d)", username, userID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  "Login berhasil",
		"user_id":  userID,
		"username": username,
	})
}

// CheckSessionHandler
func CheckSessionHandler(w http.ResponseWriter, r *http.Request) {
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		log.Printf("❌ CheckSessionHandler: Error mendapatkan sesi: %v", err)
		http.Error(w, `{"error": "Gagal mengambil informasi sesi"}`, http.StatusInternalServerError)
		return
	}

	userID, userIDOk := session.Values["user_id"].(int)
	username, usernameOk := session.Values["username"].(string)

	if session.IsNew || !userIDOk || !usernameOk || username == "" {
		log.Println("❌ CheckSessionHandler: Sesi tidak valid atau tidak ada data pengguna.")
		http.Error(w, `{"error": "Unauthorized: Sesi tidak aktif atau tidak valid"}`, http.StatusUnauthorized)
		return
	}

	log.Printf("✅ CheckSessionHandler: Sesi valid untuk pengguna '%s' (ID: %d)", username, userID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"message":  "Sesi valid",
		"user_id":  userID,
		"username": username,
		"email":    session.Values["email"],
	}
	json.NewEncoder(w).Encode(response)
}

// LogoutHandler
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { /* ... */
	}
	session, _ := Store.Get(r, "elektronik_rumah_session")
	session.Options.MaxAge = -1
	err := session.Save(r, w)
	if err != nil { /* ... */
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Logout berhasil"})
}
