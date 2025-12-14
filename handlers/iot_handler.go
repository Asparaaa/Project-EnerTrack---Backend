package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	// Menggunakan blank identifier (_) untuk menghindari error "imported and not used"
	// jika logic database MySQL atau Messaging belum aktif di handler yang dijalankan.
	_ "EnerTrack-BE/db"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	_ "firebase.google.com/go/db"
	"firebase.google.com/go/messaging"
)

// --- KONFIGURASI ---
const DEVICE_TOKEN_HP_KAMU = "fZl5mptxTx6TaYB3tSfoEn:APA91bGsmw1X093FFlw2BrWn7PnaGOLsn-iZBvznBCdW5auE1nHqXaesSkzwwKaAKF5Kam2ytqIFYVSOP3PT2lmHWYe7Wx5jl1u0HeXEpqNY4Hv7ghwRJrI"

// URL REST API RTDB (Tanpa SDK, Gratis & Cepat)
const RTDB_REST_URL = "https://enertrack-test-default-rtdb.asia-southeast1.firebasedatabase.app/sensor.json"

// ID User Default untuk Sinkronisasi (Sesuai User Login di App)
const SYNC_USER_ID = 16

// --- STRUKTUR DATA ---

// 1. Data Utama (Internal App)
type IotData struct {
	UserID      int     `json:"user_id"`
	DeviceLabel string  `json:"device_label"`
	Voltase     float64 `json:"voltase"`
	Ampere      float64 `json:"ampere"`
	Watt        float64 `json:"watt"`
	KwhTotal    float64 `json:"kwh_total"`
}

// 2. Data Respon Command (Untuk ESP32)
type CommandResponse struct {
	Status      string `json:"status"`
	Command     string `json:"command"`
	DeviceLabel string `json:"device_label"`
}

// 3. Data Mentah dari RTDB (Sesuai struktur JSON di Firebase)
type RtdbSensorData struct {
	Current float64 `json:"current"` // Ampere
	Power   float64 `json:"power"`   // Watt
	Voltage float64 `json:"voltage"` // Voltase
}

// 4. Data untuk Sync ke Firestore
type SyncData struct {
	UserID      int
	DeviceLabel string
	Voltase     float64
	Ampere      float64
	Watt        float64
	KwhTotal    float64
}

// =================================================================
// 1. IOT INPUT HANDLER (POST - Direct Device Push)
// Handler ini menerima data langsung dari ESP32 via HTTP POST
// =================================================================
func IotInputHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
		return
	}

	var data IotData
	// Mencoba decode format IotData standar
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		// Jika gagal, coba decode format RTDB sederhana (fallback)
		log.Printf("⚠️ Format JSON standar tidak cocok, mencoba format sensor raw...")
		return
	}

	// Logic simpan ke Firestore (sama seperti sync, tapi dari input langsung)
	processDataToFirestore(w, app, SyncData{
		UserID:      data.UserID,
		DeviceLabel: data.DeviceLabel,
		Voltase:     data.Voltase,
		Ampere:      data.Ampere,
		Watt:        data.Watt,
		KwhTotal:    data.KwhTotal,
	})
}

// =================================================================
// 2. GET COMMAND HANDLER (GET - Device Polling)
// Handler ini dipanggil main.go, wajib ada agar tidak error undefined
// =================================================================
func GetCommandForDeviceHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Dummy response agar valid
	resp := CommandResponse{
		Status:  "success",
		Command: "NONE",
	}
	json.NewEncoder(w).Encode(resp)
}

// =================================================================
// 3. REALTIME DB TO FIRESTORE HANDLER (GET - Manual/Scheduled Sync)
// Handler Utama untuk menarik data dari RTDB dan push ke Firestore
// =================================================================
func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed, use GET", http.StatusMethodNotAllowed)
		return
	}

	// 1. Ambil Parameter Device Label dari URL
	query := r.URL.Query()
	deviceLabel := query.Get("device_label")
	if deviceLabel == "" {
		deviceLabel = "Default Meter"
		log.Println("⚠️ Param 'device_label' kosong. Menggunakan default.")
	}

	// 2. HTTP GET ke RTDB (REST API)
	// Ini solusi bypass agar tidak perlu billing Firebase Cloud Function
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(RTDB_REST_URL)
	if err != nil {
		log.Printf("❌ Gagal GET ke RTDB: %v", err)
		http.Error(w, "Error fetching RTDB", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("❌ RTDB Error Status: %d", resp.StatusCode)
		http.Error(w, "RTDB returns error", http.StatusBadGateway)
		return
	}

	// 3. Baca & Decode JSON dari RTDB
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("❌ Gagal baca body: %v", err)
		http.Error(w, "Error reading body", http.StatusInternalServerError)
		return
	}

	var rtdbData RtdbSensorData
	if err := json.Unmarshal(bodyBytes, &rtdbData); err != nil {
		log.Printf("❌ Gagal parsing JSON RTDB: %v. Body: %s", err, string(bodyBytes))
		http.Error(w, "Invalid JSON from RTDB", http.StatusInternalServerError)
		return
	}

	// 4. Siapkan Data untuk Firestore
	syncData := SyncData{
		UserID:      SYNC_USER_ID,
		DeviceLabel: deviceLabel,
		Voltase:     rtdbData.Voltage,
		Ampere:      rtdbData.Current,
		Watt:        rtdbData.Power,
		KwhTotal:    0.0,
	}

	// 5. Proses Simpan ke Firestore
	processDataToFirestore(w, app, syncData)
}

// Fungsi Helper untuk logika update Firestore agar rapi
func processDataToFirestore(w http.ResponseWriter, app *firebase.App, data SyncData) {
	ctx := context.Background()
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Firestore: %v", err)
		http.Error(w, "Firestore Init Error", http.StatusInternalServerError)
		return
	}
	defer firestoreClient.Close()

	// Logic Notifikasi Sederhana (Optional)
	if data.Voltase > 250 {
		log.Println("⚠️ BAHAYA: Overvoltage terdeteksi!")
		sendNotification(ctx, app, "Bahaya!", fmt.Sprintf("Voltase tinggi: %.1f V", data.Voltase))
	}

	// Buat ID Dokumen Unik: user16_Nama_Device
	docID := fmt.Sprintf("user%d_%s", data.UserID, strings.ReplaceAll(data.DeviceLabel, " ", "_"))
	docRef := firestoreClient.Collection("monitoring_live").Doc(docID)

	// Cek Data Lama untuk preserve KWH Total
	snap, err := docRef.Get(ctx)
	if err == nil && snap.Exists() {
		oldData := snap.Data()
		if kwh, ok := oldData["kwh_total"].(float64); ok {
			data.KwhTotal = kwh
		} else if kwhInt, ok := oldData["kwh_total"].(int64); ok {
			data.KwhTotal = float64(kwhInt)
		}
	}

	statusDevice := "ON"
	if data.Watt == 0 {
		statusDevice = "OFF"
	}

	// Tulis ke Firestore
	_, err = docRef.Set(ctx, map[string]interface{}{
		"user_id":     data.UserID,
		"device_name": data.DeviceLabel,
		"voltase":     data.Voltase,
		"ampere":      data.Ampere,
		"watt":        data.Watt,
		"kwh_total":   data.KwhTotal,
		"status":      statusDevice,
		"last_update": firestore.ServerTimestamp,
	}, firestore.MergeAll)

	if err != nil {
		log.Printf("❌ Gagal update Firestore: %v", err)
		http.Error(w, "Firestore Write Error", http.StatusInternalServerError)
		return
	}

	// Response Sukses ke Pemanggil (Postman/Scheduler)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Data synced from RTDB to Firestore",
		"device":  data.DeviceLabel,
		"power":   data.Watt,
	})
	log.Printf("✅ Sync Sukses: %s | %.2f W", docID, data.Watt)
}

// Fungsi Helper Kirim Notif (Menggunakan library messaging yang diimport)
func sendNotification(ctx context.Context, app *firebase.App, title, body string) {
	client, err := app.Messaging(ctx)
	if err != nil {
		return
	}
	msg := &messaging.Message{
		Token: DEVICE_TOKEN_HP_KAMU,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
	}
	client.Send(ctx, msg)
}