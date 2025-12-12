package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"EnerTrack-BE/db"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	"firebase.google.com/go/messaging" // Library buat kirim notif
)

const DEVICE_TOKEN_HP_KAMU =" fZl5mptxTx6TaYB3tSfoEn:APA91bGsmw1X093FFlw2BrWn7PnaGOLsn-iZBvznBCdW5auE1nHqXaesSkzwwKaAKF5Kam2ytqIFYVSOP3PT2lmHWYe7Wx5jl1u0HeXEpqNY4Hv7ghwRJrI " 

type IotData struct {
	UserID      int     `json:"user_id"`
	DeviceLabel string  `json:"device_label"`
	Voltase     float64 `json:"voltase"`
	Ampere      float64 `json:"ampere"`
	Watt        float64 `json:"watt"`
	KwhTotal    float64 `json:"kwh_total"`
}

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
	} else {
		defer firestoreClient.Close()
	}

	messagingClient, err := app.Messaging(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Messaging: %v", err)
	}

	// --- 2. LOGIKA ALARM NOTIFIKASI (FIX) ---
	var notifTitle string
	var notifBody string
	kirimNotif := false

	// Skenario 1: Bahaya Voltase Tinggi
	if data.Voltase > 240.0 {
		notifTitle = "⚠️ DANGER: Overvoltage!"
		notifBody = fmt.Sprintf("Voltage at %s spiked to %.1f V! Check immediately.", data.DeviceLabel, data.Voltase)
		kirimNotif = true
	}

	// Skenario 2: Alat Mati (Watt 0)
	if data.Watt == 0 {
		notifTitle = "Info: Device Off"
		notifBody = fmt.Sprintf("%s is currently consuming 0 Watt (Standby).", data.DeviceLabel)
		kirimNotif = true
	}

	// EKSEKUSI KIRIM NOTIFIKASI KE HP
	if kirimNotif && messagingClient != nil && len(DEVICE_TOKEN_HP_KAMU) > 20 {
		message := &messaging.Message{
			Token: DEVICE_TOKEN_HP_KAMU, // Kirim spesifik ke HP kamu
			Notification: &messaging.Notification{
				Title: notifTitle,
				Body:  notifBody,
			},
			// Data tambahan (bisa dibaca Android di background)
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
        INSERT INTO energy_logs (user_id, device_label, voltase, ampere, watt, kwh_total) 
        VALUES (?, ?, ?, ?, ?, ?)
    `
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
		})
		if err != nil {
			log.Printf("❌ Gagal update Firestore: %v", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "message":"Data processed"}`))
}