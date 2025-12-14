package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	sqldb "EnerTrack-BE/db"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
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
// 0. CORE LOGIC: Sinkronisasi, Update Firestore, dan Notifikasi
// Fungsi ini yang akan dipanggil oleh HTTP Handler dan Scheduler
// =================================================================
func syncAndNotify(ctx context.Context, app *firebase.App, data SyncData) (status string, err error) {
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ [CORE] Gagal init Firestore: %v", err)
		return "ERROR", err
	}
	defer firestoreClient.Close()

	docID := fmt.Sprintf("user%d_%s", data.UserID, strings.ReplaceAll(data.DeviceLabel, " ", "_"))
	docRef := firestoreClient.Collection("monitoring_live").Doc(docID)

	// --- LOGIKA STATUS BARU ---
	statusDevice := "ON"
	if data.Watt < 0.1 || data.Ampere < 0.01 || data.Voltase < 1.0 {
		statusDevice = "OFF"
	}
	// --------------------------

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

	// [Logika Notifikasi dipertahankan seperti sebelumnya]
	if data.Voltase > 250 {
		shouldNotify = true
        notifTitle = "High Voltage Alert!"
        notifBody = fmt.Sprintf("Device %s detected %.1f V. Check immediately!", data.DeviceLabel, data.Voltase)
	} else if previousStatus == "ON" && statusDevice == "OFF" {
		shouldNotify = true
        notifTitle = "Device Turned OFF"
        notifBody = fmt.Sprintf("Device %s is now inactive (0 Watt/Amp/Volt).", data.DeviceLabel)
	} else if (previousStatus == "OFF" || previousStatus == "UNKNOWN") && statusDevice == "ON" {
        shouldNotify = true
        notifTitle = "Device Turned ON"
        notifBody = fmt.Sprintf("Device %s is now active.", data.DeviceLabel)
    }

	if shouldNotify {
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
		log.Printf("❌ [CORE] Gagal update Firestore: %v", err)
		return statusDevice, err
	}

	log.Printf("✅ [CORE] Sync Sukses User %d: %s | Status: %s", data.UserID, docID, statusDevice)
	return statusDevice, nil
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

    if data.UserID == 0 {
        http.Error(w, "User ID is required", http.StatusBadRequest)
        return
    }

    // Panggil fungsi inti untuk menyimpan data
	status, err := syncAndNotify(r.Context(), app, SyncData{
		UserID:      data.UserID, 
		DeviceLabel: data.DeviceLabel,
		Voltase:     data.Voltase,
		Ampere:      data.Ampere,
		Watt:        data.Watt,
	})

    if err != nil {
        http.Error(w, "Error processing data: "+err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Data saved and processed.",
		"device":  data.DeviceLabel,
		"status_device": status,
	})
}

// =================================================================
// 2. GET COMMAND HANDLER (GET - Device Polling)
// ... (Kode tetap sama)
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
// Handler ini sekarang hanya mengambil data dan memanggil fungsi inti
// =================================================================
func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed, use GET", http.StatusMethodNotAllowed)
		return
	}
    ctx := context.Background()
	query := r.URL.Query()
	deviceLabel := query.Get("device_label")
    userIDStr := query.Get("user_id")

	if deviceLabel == "" {
		deviceLabel = "Default Meter"
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
	err = rtdbClient.NewRef("sensor").Get(ctx, &rtdbData)
	if err != nil {
		log.Printf("❌ Gagal GET data dari RTDB (Admin SDK): %v", err)
		http.Error(w, "Error fetching data from RTDB", http.StatusInternalServerError)
		return
	}

	// 4. Proses Simpan ke Firestore (Panggil fungsi inti)
	status, err := syncAndNotify(ctx, app, SyncData{
		UserID:      syncUserID, 
		DeviceLabel: deviceLabel,
		Voltase:     rtdbData.Voltage,
		Ampere:      rtdbData.Current,
		Watt:        rtdbData.Power,
	})

    if err != nil {
        http.Error(w, "Error saving data: "+err.Error(), http.StatusInternalServerError)
        return
    }
    
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Data synced",
		"device":  deviceLabel,
		"status_device": status,
	})
}


// =================================================================
// 4. SCHEDULER INTERNAL (Pengganti Google Cloud Scheduler)
// =================================================================

// StartInternalScheduler memulai goroutine yang memicu sinkronisasi RTDB->Firestore secara berkala.
// Harus dipanggil sekali saat aplikasi Go kamu (main.go) pertama kali dijalankan.
func StartInternalScheduler(app *firebase.App, syncUserID int, deviceLabel string, interval time.Duration) {
    // Jalankan scheduler di goroutine background
    go func() {
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        log.Printf("⏰ Scheduler Internal dimulai, sync data setiap %v...", interval)
        
        for {
            select {
            case <-ticker.C:
                // Cek apakah UserID valid
                if syncUserID > 0 {
                    log.Println("--- Memicu Sinkronisasi Terjadwal ---")
                    // Gunakan context baru dengan timeout untuk memastikan operasi tidak menggantung
                    ctx, cancel := context.WithTimeout(context.Background(), 15 * time.Second)
                    
                    // 1. Inisialisasi RTDB Client
                    rtdbClient, err := app.Database(ctx)
                    if err != nil {
                        log.Printf("❌ [SCHEDULER] Gagal init RTDB Client: %v", err)
                        cancel()
                        continue
                    }

                    // 2. Ambil data dari path "sensor" di RTDB
                    var rtdbData RtdbSensorData
                    err = rtdbClient.NewRef("sensor").Get(ctx, &rtdbData)
                    
                    if err != nil {
                        log.Printf("❌ [SCHEDULER] Gagal GET data dari RTDB: %v", err)
                        cancel()
                        continue
                    }
                    
                    // 3. Panggil fungsi inti untuk proses dan simpan ke Firestore
                    syncAndNotify(ctx, app, SyncData{
                        UserID:      syncUserID, 
                        DeviceLabel: deviceLabel,
                        Voltase:     rtdbData.Voltage,
                        Ampere:      rtdbData.Current,
                        Watt:        rtdbData.Power,
                    })
                    
                    cancel() // Bebaskan context
                } else {
                    log.Println("⚠️ Scheduler tidak berjalan: UserID tidak valid (0). Pastikan nilai diisi.")
                }
            }
        }
    }()
}


// =================================================================
// FUNGSI BANTU (Tidak diubah)
// =================================================================

// Fungsi Bantu: Ambil Token dari MySQL
func getUserFcmTokenFromDB(userID int) string {
	var token string
	query := "SELECT fcm_token FROM users WHERE user_id = ?"
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