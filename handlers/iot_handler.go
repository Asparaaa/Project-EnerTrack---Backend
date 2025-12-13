package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv" // Diperlukan untuk GetCommandForDeviceHandler
	"strings" // Diperlukan untuk GetCommandForDeviceHandler

	"EnerTrack-BE/db"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	"firebase.google.com/go/messaging" // Library buat kirim notif
)

// FIX: Hapus spasi di awal dan akhir token. SEBAIKNYA AMBIL DARI DB BERDASARKAN USER ID!
const DEVICE_TOKEN_HP_KAMU = "fZl5mptxTx6TaYB3tSfoEn:APA91bGsmw1X093FFlw2BrWn7PnaGOLsn-iZBvznBCdW5auE1nHqXaesSkzwwKaAKF5Kam2ytqIFYVSOP3PT2lmHWYe7Wx5jl1u0HeXEpqNY4Hv7ghwRJrI" 

type IotData struct {
	UserID      int     `json:"user_id"`
	DeviceLabel string  `json:"device_label"`
	Voltase     float64 `json:"voltase"`
	Ampere      float64 `json:"ampere"`
	Watt        float64 `json:"watt"`
	KwhTotal    float64 `json:"kwh_total"`
}

// Struktur yang akan dikirim kembali ke ESP untuk Polling Command (GET)
type CommandResponse struct {
	Status string `json:"status"` // "success" atau "error"
	Command string `json:"command"` // Contoh: "RELAY_ON", "RELAY_OFF", atau "NONE"
	DeviceLabel string `json:"device_label"`
}

// =================================================================
// 1. IOT INPUT HANDLER (POST - Arduino Push Data)
// =================================================================

// Handler menerima *firebase.App supaya bisa akses Messaging & Firestore
func IotInputHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
		return
	}

	var data IotData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("❌ Error decode JSON IoT: %v", err)
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	// 1. Siapkan Client (Firestore & Messaging)
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Firestore: %v", err)
	}

	messagingClient, err := app.Messaging(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Messaging: %v", err)
	}

	if firestoreClient != nil {
		defer firestoreClient.Close() 
	}


	// --- 2. LOGIKA ALARM NOTIFIKASI ---
	var notifTitle string
	var notifBody string
	kirimNotif := false

	// Skenario 1: Bahaya Voltase Tinggi
	if data.Voltase > 240.0 {
		notifTitle = "⚠️ DANGER: Overvoltage!"
		notifBody = fmt.Sprintf("Voltage at %s spiked to %.1f V! Check immediately.", data.DeviceLabel, data.Voltase)
		kirimNotif = true
	}

	// Skenario 2: Alat Mati (Watt 0) - Dibuat false agar tidak spam notif standby
	if data.Watt == 0 {
		notifTitle = "Info: Device Off"
		notifBody = fmt.Sprintf("%s is currently consuming 0 Watt (Standby).", data.DeviceLabel)
		kirimNotif = false 
	}

	// EKSEKUSI KIRIM NOTIFIKASI KE HP
	if kirimNotif && messagingClient != nil && len(DEVICE_TOKEN_HP_KAMU) > 20 {
		message := &messaging.Message{
			Token: DEVICE_TOKEN_HP_KAMU, // Kirim spesifik ke HP kamu
			Notification: &messaging.Notification{
				Title: notifTitle,
				Body:  notifBody,
			},
			Data: map[string]string{
				"status": "alert",
				"device": data.DeviceLabel,
			},
		}

		response, err := messagingClient.Send(ctx, message)
		if err != nil {
			log.Printf("❌ Gagal kirim notif FCM: %v", err)
		} else {
			log.Printf("✅ Notifikasi sukses dikirim! ID: %s", response)
		}
	}
	// ----------------------------------------

	// 3. Simpan ke MySQL (History)
	query := `
        INSERT INTO energy_logs (user_id, device_label, voltase, ampere, watt, kwh_total, created_at) 
        VALUES (?, ?, ?, ?, ?, ?, NOW())
    `
    // FIX: Tambahkan kolom created_at (timestamp di MySQL)
	_, err = db.DB.Exec(query, data.UserID, data.DeviceLabel, data.Voltase, data.Ampere, data.Watt, data.KwhTotal)
	if err != nil {
		log.Printf("❌ Gagal simpan MySQL: %v", err)
	}

	// 4. Update Firestore (Realtime UI)
	if firestoreClient != nil {
		docID := fmt.Sprintf("user%d_%s", data.UserID, data.DeviceLabel)
		statusDevice := "ON"
		if data.Watt == 0 {
			statusDevice = "OFF"
		}

		_, err = firestoreClient.Collection("monitoring_live").Doc(docID).Set(ctx, map[string]interface{}{
			"user_id":     data.UserID,
			"device_name": data.DeviceLabel,
			"voltase":     data.Voltase,
			"ampere":      data.Ampere,
			"watt":        data.Watt,
			"kwh_total":   data.KwhTotal,
			"status":      statusDevice,
			"last_update": firestore.ServerTimestamp,
		}, firestore.MergeAll) // FIX: Gunakan MergeAll agar tidak menimpa field lain
		if err != nil {
			log.Printf("❌ Gagal update Firestore: %v", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "message":"Data processed"}`))
}

// =================================================================
// 2. GET COMMAND FOR DEVICE HANDLER (GET - Arduino Pull Command)
// =================================================================

// GetCommandForDeviceHandler: Handler GET. ESP32/Arduino akan memanggil ini untuk cek perintah.
// Contoh URL: /api/iot/command?user_id=11&device=Pair%20Small%20Meter
func GetCommandForDeviceHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed, use GET", http.StatusMethodNotAllowed)
		return
	}

	// 1. Ambil parameter dari URL Query
	query := r.URL.Query()
	deviceLabel := query.Get("device")
	userIDStr := query.Get("user_id")

	if deviceLabel == "" || userIDStr == "" {
		http.Error(w, "Missing device or user_id query parameter", http.StatusBadRequest)
		return
	}

	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		http.Error(w, "Invalid user_id format", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Firestore untuk Command: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer firestoreClient.Close()

	// 2. Tentukan Document ID dan Ambil Perintah
	docID := fmt.Sprintf("user%d_%s", userID, deviceLabel)
	
	docRef := firestoreClient.Collection("device_commands").Doc(docID)
	docSnap, err := docRef.Get(ctx)

	command := "NONE" // Default: tidak ada perintah
	if err == nil && docSnap.Exists() {
		data := docSnap.Data()
		if cmd, ok := data["pending_command"].(string); ok && cmd != "NONE" {
			command = strings.ToUpper(cmd)
			
			// 3. Reset Perintah setelah diambil oleh ESP (agar tidak berulang)
			_, updateErr := docRef.Set(ctx, map[string]interface{}{
				"pending_command": "NONE", // Reset perintah menjadi NONE
				"last_sent": firestore.ServerTimestamp, // Catat waktu perintah terakhir dikirim
			}, firestore.MergeAll) 

			if updateErr != nil {
				log.Printf("❌ Gagal reset command di Firestore: %v", updateErr)
			}
		}
	} else if err != nil && strings.Contains(err.Error(), "not found") {
		// Dokumen perintah belum ada, biarkan command tetap "NONE"
	} else if err != nil {
		log.Printf("❌ Gagal mengambil dokumen command Firestore: %v", err)
	}

	// 4. Kirim Respon JSON ke ESP
	response := CommandResponse{
		Status: "success",
		Command: command,
		DeviceLabel: deviceLabel,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
	log.Printf("✅ Perintah untuk %s dikirim: %s", deviceLabel, command)
}