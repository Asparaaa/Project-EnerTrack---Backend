package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"EnerTrack-BE/db"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	_ "firebase.google.com/go/db"      // FIX: Tambah import Realtime Database
	"firebase.google.com/go/messaging" // Library buat kirim notif
)

// FIX: Hapus spasi di awal dan akhir token. SEBAIKNYA AMBIL DARI DB BERDASARKAN USER ID!
const DEVICE_TOKEN_HP_KAMU = "fZl5mptxTx6TaYB3tSfoEn:APA91bGsmw1X093FFlw2BrWn7PnaGOLsn-iZBvznBCdW5auE1nHqXaesSkzwwKaAKF5Kam2ytqIFYVSOP3PT2lmHWYe7Wx5jl1u0HeXEpqNY4Hv7ghwRJrI" 

// --- STRUKTUR DATA UTAMA ---
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

// --- STRUKTUR DATA RTDB SINKRONISASI (BARU) ---
// Struktur data yang ada di Firebase Realtime Database (/sensor)
type RtdbSensorData struct {
	Current float64 `json:"current"` // Akan dimapping ke Ampere
	Power   float64 `json:"power"`   // Akan dimapping ke Watt
	Voltage float64 `json:"voltage"` // Akan dimapping ke Voltase
}

// Struktur data yang akan disimpan ke Firestore
type SyncData struct {
	UserID      int
	DeviceLabel string
	Voltase     float64
	Ampere      float64
	Watt        float64
	KwhTotal    float64 
}

// =================================================================
// 3. REALTIME DB TO FIRESTORE HANDLER (GET - Manual Sync)
// =================================================================

// RealtimeDBToFirestoreHandler: Handler GET untuk membaca data dari RTDB dan menuliskannya ke Firestore
func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed, use GET", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()
	
	// ID user yang sedang login (hardcoded 16 berdasarkan log terakhir)
	const syncUserID = 16 
    
    // PERUBAHAN KRITIS: Ambil device label dari query parameter
    query := r.URL.Query()
	deviceLabel := query.Get("device_label")

    if deviceLabel == "" {
        // Jika tidak ada label, gunakan label default
        deviceLabel = "Default Meter" 
        log.Println("⚠️ Query parameter 'device_label' kosong. Menggunakan 'Default Meter'.")
    }

	// 1. Inisialisasi Klien Realtime Database
	rtdbClient, err := app.Database(ctx)
	if err != nil {
		log.Printf("❌ Gagal init RTDB Client: %v", err)
		http.Error(w, "Internal Server Error (RTDB Init)", http.StatusInternalServerError)
		return
	}

	// 2. Baca data dari path /sensor di RTDB (asumsi semua sensor masih mengirim ke path ini)
	ref := rtdbClient.NewRef("sensor")
	var rtdbData RtdbSensorData
	
	if err := ref.Get(ctx, &rtdbData); err != nil {
		log.Printf("❌ Gagal membaca data dari RTDB /sensor: %v", err)
		http.Error(w, "Internal Server Error (RTDB Read)", http.StatusInternalServerError)
		return
	}

	// 3. Mapping Data (RTDB field name -> Firestore/App field name)
	syncData := SyncData{
		UserID:      syncUserID,
		DeviceLabel: deviceLabel, // Menggunakan label dari query
		Voltase:     rtdbData.Voltage,
		Ampere:      rtdbData.Current,
		Watt:        rtdbData.Power,
		KwhTotal:    0.0, 
	}

	// 4. Ambil KwhTotal terakhir dari Firestore 
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Firestore Client: %v", err)
		http.Error(w, "Internal Server Error (Firestore Init)", http.StatusInternalServerError)
		return
	}
	defer firestoreClient.Close()
	
	// Doc ID sekarang menggunakan label dari query parameter
	docID := fmt.Sprintf("user%d_%s", syncData.UserID, strings.ReplaceAll(syncData.DeviceLabel, " ", "_")) // Bersihkan spasi
	docRef := firestoreClient.Collection("monitoring_live").Doc(docID)
	
	snap, err := docRef.Get(ctx)
	if err == nil && snap.Exists() {
		dataMap := snap.Data()
		if kwh, ok := dataMap["kwh_total"].(float64); ok {
			syncData.KwhTotal = kwh 
		} else if kwhInt, ok := dataMap["kwh_total"].(int64); ok {
			syncData.KwhTotal = float64(kwhInt) 
		}
	}

	// 5. Update Firestore
	statusDevice := "ON"
	if syncData.Watt == 0 {
		statusDevice = "OFF"
	}

	_, err = docRef.Set(ctx, map[string]interface{}{
		"user_id":     syncData.UserID,
		"device_name": syncData.DeviceLabel,
		"voltase":     syncData.Voltase,
		"ampere":      syncData.Ampere,
		"watt":        syncData.Watt,
		"kwh_total":   syncData.KwhTotal,
		"status":      statusDevice,
		"last_update": firestore.ServerTimestamp,
	}, firestore.MergeAll)
	
	if err != nil {
		log.Printf("❌ Gagal update Firestore dari RTDB: %v", err)
		http.Error(w, "Internal Server Error (Firestore Write)", http.StatusInternalServerError)
		return
	}

	// 6. Respon Sukses
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]string{
		"status": "success",
		"message": "RTDB data synchronized to Firestore",
		"device": syncData.DeviceLabel,
		"power": strconv.FormatFloat(syncData.Watt, 'f', 2, 64) + "W",
	}
	json.NewEncoder(w).Encode(response)
	log.Printf("✅ RTDB Sync berhasil. Perangkat: %s, Power: %.2f W", syncData.DeviceLabel, syncData.Watt)
}
// ... (omitted for brevity)
// =================================================================
// 2. GET COMMAND FOR DEVICE HANDLER (GET - Arduino Pull Command)
// ... (omitted for brevity)