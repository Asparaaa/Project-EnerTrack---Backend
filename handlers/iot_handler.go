package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io" // <-- DIKEMBALIKAN untuk membaca body HTTP
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

// --- KONFIGURASI REST API KHUSUS SCHEDULER ---
// Karena Admin SDK Go bermasalah dengan URL regional, kita pakai REST API.
const RTDB_REST_URL = "https://enertrack-test-default-rtdb.asia-southeast1.firebasedatabase.app/sensor.json"

// --- STRUKTUR DATA (SAMA) ---

type IotData struct {
	UserID      int     `json:"user_id"`
	DeviceLabel string  `json:"device_label"`
	Voltase     float64 `json:"voltase"`
	Ampere      float64 `json:"ampere"`
	Watt        float64 `json:"watt"`
}

type CommandResponse struct {
	Status      string `json:"status"`
	Command     string `json:"command"`
	DeviceLabel string `json:"device_label"`
}

type RtdbSensorData struct {
	Current float64 `json:"current"` // Ampere
	Power   float64 `json:"power"`   // Watt
	Voltage float64 `json:"voltage"` // Voltase
}

type SyncData struct {
	UserID      int
	DeviceLabel string
	Voltase     float64
	Ampere      float64
	Watt        float64
}

// =================================================================
// 0. CORE LOGIC: Sinkronisasi, Update Firestore, dan Notifikasi (SAMA)
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

	statusDevice := "ON"
	if data.Watt < 0.1 || data.Ampere < 0.01 || data.Voltase < 1.0 {
		statusDevice = "OFF"
	}

	var previousStatus string = "UNKNOWN"
	snap, err := docRef.Get(ctx)
	if err == nil && snap.Exists() {
		oldData := snap.Data()
		if status, ok := oldData["status"].(string); ok {
			previousStatus = status
		}
	}

	shouldNotify := false
    var notifTitle string
    var notifBody string

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
// 1. IOT INPUT HANDLER (POST - Direct Device Push) (SAMA)
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
// 2. GET COMMAND HANDLER (GET - Device Polling) (SAMA)
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
// Di sini kita kembali menggunakan Admin SDK (yang mungkin gagal)
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

	// 2. HTTP GET ke RTDB (Mengganti Admin SDK untuk handler ini juga)
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(RTDB_REST_URL)
	if err != nil {
		log.Printf("❌ Gagal HTTP GET ke RTDB: %v", err)
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
		"message": "Data synced via REST API",
		"device":  deviceLabel,
		"status_device": status,
	})
}


// =================================================================
// 4. SCHEDULER INTERNAL (Pengganti Google Cloud Scheduler)
// Menggunakan HTTP GET Request (REST API) untuk koneksi RTDB.
// =================================================================

func StartInternalScheduler(app *firebase.App, syncUserID int, deviceLabel string, interval time.Duration) {
    go func() {
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        log.Printf("⏰ Scheduler Internal dimulai, sync data setiap %v...", interval)
        
        for {
            select {
            case <-ticker.C:
                if syncUserID > 0 {
                    log.Println("--- Memicu Sinkronisasi Terjadwal (via REST API) ---")
                    ctx, cancel := context.WithTimeout(context.Background(), 15 * time.Second)
                    
                    // 1. HTTP GET ke RTDB (MENGGANTIKAN ADMIN SDK)
                    client := http.Client{Timeout: 10 * time.Second}
                    resp, err := client.Get(RTDB_REST_URL)
                    
                    if err != nil {
                        log.Printf("❌ [SCHEDULER] Gagal HTTP GET ke RTDB: %v", err)
                        cancel()
                        continue
                    }
                    defer resp.Body.Close()

                    if resp.StatusCode != http.StatusOK {
                        log.Printf("❌ [SCHEDULER] RTDB Error Status: %d", resp.StatusCode)
                        cancel()
                        continue
                    }

                    // 2. Baca & Decode JSON
                    bodyBytes, err := io.ReadAll(resp.Body)
                    if err != nil {
                        log.Printf("❌ [SCHEDULER] Gagal baca body: %v", err)
                        cancel()
                        continue
                    }

                    var rtdbData RtdbSensorData
                    if err := json.Unmarshal(bodyBytes, &rtdbData); err != nil {
                        log.Printf("❌ [SCHEDULER] Gagal parsing JSON RTDB: %v", err)
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
// FUNGSI BANTU (TIDAK DIUBAH)
// =================================================================

func getUserFcmTokenFromDB(userID int) string {
	var token string
	query := "SELECT fcm_token FROM users WHERE user_id = ?"
	err := sqldb.DB.QueryRow(query, userID).Scan(&token)
	if err != nil {
		return ""
	}
	return token
}

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