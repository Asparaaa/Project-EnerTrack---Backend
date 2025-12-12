package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"EnerTrack-BE/db"

	"cloud.google.com/go/firestore"
)

type IotData struct {
	UserID      int     `json:"user_id"`
	DeviceLabel string  `json:"device_label"`
	Voltase     float64 `json:"voltase"`
	Ampere      float64 `json:"ampere"`
	Watt        float64 `json:"watt"`
	KwhTotal    float64 `json:"kwh_total"`
}

func IotInputHandler(w http.ResponseWriter, r *http.Request, fs *firestore.Client) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
		return
	}

	var data IotData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("❌ Error decode JSON IoT: %v", err)
		http.Error(w, "Format JSON tidak valid", http.StatusBadRequest)
		return
	}

	if data.UserID == 0 || data.DeviceLabel == "" {
		http.Error(w, "user_id dan device_label wajib diisi", http.StatusBadRequest)
		return
	}

	// --- A. SIMPAN KE MYSQL ---
	query := `
        INSERT INTO energy_logs (user_id, device_label, voltase, ampere, watt, kwh_total) 
        VALUES (?, ?, ?, ?, ?, ?)
    `
	_, err := db.DB.Exec(query, data.UserID, data.DeviceLabel, data.Voltase, data.Ampere, data.Watt, data.KwhTotal)

	if err != nil {
		log.Printf("❌ Gagal simpan ke MySQL: %v", err)
	} else {
		log.Printf("✅ Data tersimpan di MySQL (User: %d, Alat: %s)", data.UserID, data.DeviceLabel)
	}

	// --- B. UPDATE KE FIREBASE ---
	if fs != nil {
		ctx := context.Background()
		docID := fmt.Sprintf("user%d_%s", data.UserID, data.DeviceLabel)

		// --- LOGIKA BARU: Tentukan Status berdasarkan Watt ---
		statusDevice := "OFF"
		if data.Watt > 0 {
			statusDevice = "ON"
		}
		// ----------------------------------------------------

		_, err = fs.Collection("monitoring_live").Doc(docID).Set(ctx, map[string]interface{}{
			"user_id":     data.UserID,
			"device_name": data.DeviceLabel,
			"voltase":     data.Voltase,
			"ampere":      data.Ampere,
			"watt":        data.Watt,
			"kwh_total":   data.KwhTotal,
			"status":      statusDevice,              // Pakai variable statusDevice
			"last_update": firestore.ServerTimestamp,
		})

		if err != nil {
			log.Printf("❌ Gagal update Firebase: %v", err)
		} else {
			log.Printf("✅ Data terupdate di Firebase Doc: %s (Status: %s)", docID, statusDevice)
		}
	} else {
		log.Println("⚠️ Firestore Client bernilai nil (belum connect), skip update Firebase.")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "message":"Data IoT received and processed"}`))
}