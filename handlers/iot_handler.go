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

// 1. Data Utama (Internal App) - KwhTotal DIHAPUS
type IotData struct {
	UserID      int     `json:"user_id"`
	DeviceLabel string  `json:"device_label"`
	Voltase     float64 `json:"voltase"`
	Ampere      float64 `json:"ampere"`
	Watt        float64 `json:"watt"`
}

// 2. Data Respon Command (Untuk ESP32)
type CommandResponse struct {
	Status      string `json:"status"`
	Command     string `json:"command"`
	DeviceLabel string `json:"device_label"`
}

// 3. Data Mentah dari RTDB
type RtdbSensorData struct {
	Current float64 `json:"current"` // Ampere
	Power   float64 `json:"power"`   // Watt
	Voltage float64 `json:"voltage"` // Voltase
}

// 4. Data untuk Sync ke Firestore - KwhTotal DIHAPUS
type SyncData struct {
	UserID      int
	DeviceLabel string
	Voltase     float64
	Ampere      float64
	Watt        float64
}

// =================================================================
// 1. IOT INPUT HANDLER (POST - Direct Device Push)
// =================================================================
func IotInputHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
		return
	}

	var data IotData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("⚠️ Format JSON standar tidak cocok, cek format...")
		return
	}

	// Logic simpan ke Firestore (KwhTotal dihapus)
	processDataToFirestore(w, app, SyncData{
		UserID:      data.UserID,
		DeviceLabel: data.DeviceLabel,
		Voltase:     data.Voltase,
		Ampere:      data.Ampere,
		Watt:        data.Watt,
	})
}

// =================================================================
// 2. GET COMMAND HANDLER (GET - Device Polling)
// =================================================================
func GetCommandForDeviceHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := CommandResponse{
		Status:  "success",
		Command: "NONE",
	}
	json.NewEncoder(w).Encode(resp)
}

// =================================================================
// 3. REALTIME DB TO FIRESTORE HANDLER (GET - Sync)
// =================================================================
func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed, use GET", http.StatusMethodNotAllowed)
		return
	}

	// 1. Ambil Parameter Device Label
	query := r.URL.Query()
	deviceLabel := query.Get("device_label")
	if deviceLabel == "" {
		deviceLabel = "Default Meter"
		log.Println("⚠️ Param 'device_label' kosong. Menggunakan default.")
	}

	// 2. HTTP GET ke RTDB
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

	// 3. Baca & Decode JSON
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("❌ Gagal baca body: %v", err)
		http.Error(w, "Error reading body", http.StatusInternalServerError)
		return
	}

	var rtdbData RtdbSensorData
	if err := json.Unmarshal(bodyBytes, &rtdbData); err != nil {
		log.Printf("❌ Gagal parsing JSON RTDB: %v", err)
		http.Error(w, "Invalid JSON from RTDB", http.StatusInternalServerError)
		return
	}

	// 4. Siapkan Data (Tanpa KwhTotal)
	syncData := SyncData{
		UserID:      SYNC_USER_ID,
		DeviceLabel: deviceLabel,
		Voltase:     rtdbData.Voltage,
		Ampere:      rtdbData.Current,
		Watt:        rtdbData.Power,
	}

	// 5. Proses Simpan
	processDataToFirestore(w, app, syncData)
}

// Fungsi Helper Update Firestore & Notifikasi
func processDataToFirestore(w http.ResponseWriter, app *firebase.App, data SyncData) {
	ctx := context.Background()
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Firestore: %v", err)
		http.Error(w, "Firestore Init Error", http.StatusInternalServerError)
		return
	}
	defer firestoreClient.Close()

	docID := fmt.Sprintf("user%d_%s", data.UserID, strings.ReplaceAll(data.DeviceLabel, " ", "_"))
	docRef := firestoreClient.Collection("monitoring_live").Doc(docID)

	// --- LOGIKA STATUS BARU (STRICT) ---
	// Default status ON
	statusDevice := "ON"
	
	// Jika SALAH SATU dari parameter listrik mati/nol, maka status dianggap OFF.
	// Kita pakai ambang batas kecil (0.1) untuk antisipasi noise sensor.
	if data.Watt < 0.1 || data.Ampere < 0.01 || data.Voltase < 1.0 {
		statusDevice = "OFF"
	}
	// -----------------------------------

	// Cek Status Lama di Firestore (untuk logika notifikasi perubahan)
	var previousStatus string = "UNKNOWN"
	
	snap, err := docRef.Get(ctx)
	if err == nil && snap.Exists() {
		oldData := snap.Data()
		if status, ok := oldData["status"].(string); ok {
			previousStatus = status
		}
	}

	// --- LOGIKA NOTIFIKASI (ENGLISH) ---
	shouldNotify := false
    var notifTitle string
    var notifBody string

	// Kondisi 1: Voltase Tinggi
	if data.Voltase > 250 {
		shouldNotify = true
        notifTitle = "High Voltage Alert!"
        notifBody = fmt.Sprintf("Device %s detected %.1f V. Check immediately!", data.DeviceLabel, data.Voltase)
	}
	
    // Kondisi 2: Perangkat Mati (Transisi dari ON ke OFF)
	if previousStatus == "ON" && statusDevice == "OFF" {
		shouldNotify = true
        notifTitle = "Device Turned OFF"
        notifBody = fmt.Sprintf("Device %s is now inactive (0 Watt/Amp/Volt).", data.DeviceLabel)
	}

    // Kondisi 3: Perangkat Nyala (Transisi dari OFF ke ON)
    if (previousStatus == "OFF" || previousStatus == "UNKNOWN") && statusDevice == "ON" {
        shouldNotify = true
        notifTitle = "Device Turned ON"
        notifBody = fmt.Sprintf("Device %s is now active and consuming power.", data.DeviceLabel)
    }

	if shouldNotify {
		// Kirim notifikasi menggunakan token hardcoded (karena kita belum implementasi ambil dari DB)
		log.Printf("🔔 Sending Notification: %s", notifTitle)
		sendNotification(ctx, app, notifTitle, notifBody)
	}
	// -----------------------------------

	// Tulis ke Firestore (Field kwh_total HILANG)
	_, err = docRef.Set(ctx, map[string]interface{}{
		"user_id":     data.UserID,
		"device_name": data.DeviceLabel,
		"voltase":     data.Voltase,
		"ampere":      data.Ampere,
		"watt":        data.Watt,
		"status":      statusDevice,
		"last_update": firestore.ServerTimestamp,
	}, firestore.MergeAll)

	if err != nil {
		log.Printf("❌ Gagal update Firestore: %v", err)
		http.Error(w, "Firestore Write Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Data synced (No KWh)",
		"device":  data.DeviceLabel,
		"power":   data.Watt,
		"status_device": statusDevice,
	})
	log.Printf("✅ Sync Sukses: %s | %.2f W | Status: %s", docID, data.Watt, statusDevice)
}

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