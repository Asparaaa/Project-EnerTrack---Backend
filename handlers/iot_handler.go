package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	sqldb "EnerTrack-BE/db" // [PERBAIKAN]: Rename import SQL DB menjadi sqldb

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"

	// Import Realtime Database Client

	"firebase.google.com/go/messaging"
)

// --- STRUKTUR DATA ---

// 1. Data Utama (Internal App)
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
	// Jika perangkat IoT mengirim timestamp, uncomment baris bawah:
	// Timestamp int64   `json:"timestamp"` 
}

// 4. Data untuk Sync ke Firestore
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

    // Validasi User ID
    if data.UserID == 0 {
        http.Error(w, "User ID is required", http.StatusBadRequest)
        return
    }

	// Logic simpan ke Firestore (KwhTotal dihapus)
    // Disini UserID diambil DARI DATA JSON, bukan hardcode.
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
// Fungsi ini kini menggunakan Admin SDK untuk akses RTDB
// =================================================================
func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed, use GET", http.StatusMethodNotAllowed)
		return
	}
    ctx := context.Background()

	// 1. Ambil Parameter Device Label DAN User ID
	query := r.URL.Query()
	deviceLabel := query.Get("device_label")
    userIDStr := query.Get("user_id")

	if deviceLabel == "" {
		deviceLabel = "Default Meter"
		log.Println("⚠️ Param 'device_label' kosong. Menggunakan default.")
	}

    syncUserID := 0 
    if userIDStr != "" {
        if id, err := strconv.Atoi(userIDStr); err == nil {
            syncUserID = id
        }
    }
    
    if syncUserID == 0 {
        http.Error(w, "Parameter 'user_id' is required for sync", http.StatusBadRequest)
        return
    }

	// 2. Inisialisasi RTDB Client
    rtdbClient, err := app.Database(ctx)
	if err != nil {
		log.Printf("❌ Gagal init RTDB Client: %v", err)
		http.Error(w, "Error initializing RTDB client", http.StatusInternalServerError)
		return
	}
	
	// 3. Ambil data dari path "sensor" di RTDB menggunakan Admin SDK
	var rtdbData RtdbSensorData
    // Menggunakan ref.Get(ctx, &rtdbData) untuk langsung mengambil dan mengisi struct
	err = rtdbClient.NewRef("sensor").Get(ctx, &rtdbData)
	if err != nil {
		// [PERBAIKAN]: Hilangkan db.IsNotFound yang menyebabkan error kompilasi
		log.Printf("❌ Gagal GET data dari RTDB (Admin SDK): %v", err)
		http.Error(w, "Error fetching data from RTDB", http.StatusInternalServerError)
		return
	}

	// 4. Siapkan Data (Langkah 4, 5, dan seterusnya tetap sama)
	syncData := SyncData{
		UserID:      syncUserID, // Pake ID dari query param
		DeviceLabel: deviceLabel,
		Voltase:     rtdbData.Voltage,
		Ampere:      rtdbData.Current,
		Watt:        rtdbData.Power,
	}

	// 5. Proses Simpan ke Firestore
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
	statusDevice := "ON"
	if data.Watt < 0.1 || data.Ampere < 0.01 || data.Voltase < 1.0 {
		statusDevice = "OFF"
	}
	// -----------------------------------

	// Cek Status Lama
	var previousStatus string = "UNKNOWN"
	snap, err := docRef.Get(ctx)
	if err == nil && snap.Exists() {
		oldData := snap.Data()
		if status, ok := oldData["status"].(string); ok {
			previousStatus = status
		}
	}

	// --- LOGIKA NOTIFIKASI DINAMIS ---
	shouldNotify := false
    var notifTitle string
    var notifBody string

	// Kondisi 1: Voltase Tinggi
	if data.Voltase > 250 {
		shouldNotify = true
        notifTitle = "High Voltage Alert!"
        notifBody = fmt.Sprintf("Device %s detected %.1f V. Check immediately!", data.DeviceLabel, data.Voltase)
	}
	
    // Kondisi 2: Device OFF
	if previousStatus == "ON" && statusDevice == "OFF" {
		shouldNotify = true
        notifTitle = "Device Turned OFF"
        notifBody = fmt.Sprintf("Device %s is now inactive (0 Watt/Amp/Volt).", data.DeviceLabel)
	}

    // Kondisi 3: Device ON
    if (previousStatus == "OFF" || previousStatus == "UNKNOWN") && statusDevice == "ON" {
        shouldNotify = true
        notifTitle = "Device Turned ON"
        notifBody = fmt.Sprintf("Device %s is now active.", data.DeviceLabel)
    }

	if shouldNotify {
        // Ambil token dari DB sesuai UserID yang dikirim
		userToken := getUserFcmTokenFromDB(data.UserID)
		if userToken != "" {
			log.Printf("🔔 Sending Notification to User %d: %s", data.UserID, notifTitle)
			sendNotification(ctx, app, userToken, notifTitle, notifBody)
		} else {
			log.Printf("❌ Token not found for User %d in DB", data.UserID)
		}
	}
	// -----------------------------------

	// Tulis ke Firestore
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
		"message": "Data synced",
		"device":  data.DeviceLabel,
		"status_device": statusDevice,
	})
	log.Printf("✅ Sync Sukses User %d: %s | Status: %s", data.UserID, docID, statusDevice)
}

// Fungsi Bantu: Ambil Token dari MySQL
func getUserFcmTokenFromDB(userID int) string {
	var token string
	query := "SELECT fcm_token FROM users WHERE user_id = ?"
    // [PERBAIKAN]: Menggunakan sqldb.DB
	err := sqldb.DB.QueryRow(query, userID).Scan(&token)
	if err != nil {
		return ""
	}
	return token
}

// Fungsi Bantu: Kirim FCM
func sendNotification(ctx context.Context, app *firebase.App, token, title, body string) {
	client, err := app.Messaging(ctx)
	if err != nil {
		log.Printf("❌ Gagal init Messaging client: %v", err)
		return
	}

	msg := &messaging.Message{
		Token: token, 
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: map[string]string{
			"title": title,
			"body":  body,
			"type":  "alert",
		},
	}

	response, err := client.Send(ctx, msg)
	if err != nil {
		log.Printf("❌ Gagal kirim notif: %v", err)
	} else {
		log.Printf("✅ Notification sent to %s... ID: %s", token[:10], response)
	}
}