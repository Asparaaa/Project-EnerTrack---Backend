package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"log"
	"net/http"
)

// Struct untuk menerima data update dari frontend
type UpdateProfileRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

// UpdateUserProfileHandler menangani pembaruan data profil pengguna
func UpdateUserProfileHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, `{"error": "Metode tidak diizinkan"}`, http.StatusMethodNotAllowed)
		return
	}

	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil || session.IsNew {
		http.Error(w, `{"error": "Sesi tidak valid atau tidak ditemukan"}`, http.StatusUnauthorized)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, `{"error": "User ID tidak ditemukan di sesi"}`, http.StatusUnauthorized)
		return
	}

	var req UpdateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Data request tidak valid"}`, http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Email == "" {
		http.Error(w, `{"error": "Username dan email tidak boleh kosong"}`, http.StatusBadRequest)
		return
	}

	// Update kolom 'username' dengan nilai username yang baru
	query := "UPDATE users SET username = ?,  email = ? WHERE user_id = ?"
	result, err := db.DB.Exec(query, req.Username, req.Email, userID)

	if err != nil {
		log.Printf("❌ Gagal mengupdate profil untuk user_id %d: %v", userID, err)
		http.Error(w, `{"error": "Gagal memperbarui profil di database"}`, http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, `{"error": "User tidak ditemukan"}`, http.StatusNotFound)
		return
	}

	// Update juga data di sesi jika ada perubahan
	session.Values["username"] = req.Username
	session.Values["email"] = req.Email
	if err := session.Save(r, w); err != nil {
		log.Printf("⚠️ Gagal menyimpan sesi setelah update profil untuk user_id %d: %v", userID, err)
	}

	log.Printf("✅ Profil untuk user_id %d berhasil diperbarui.", userID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Profil berhasil diperbarui",
	})
}
