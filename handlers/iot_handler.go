package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// --- KONFIGURASI REST API RTDB ---
const RTDB_REST_URL = "https://enertrack-test-default-rtdb.asia-southeast1.firebasedatabase.app/sensor.json"

// --- STRUKTUR DATA ---
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
	Current float64 `json:"current"` 
	Power   float64 `json:"power"`   
	Voltage float64 `json:"voltage"` 
}

type SyncData struct {
	UserID      int
	DeviceLabel string
	Voltase     float64
	Ampere      float64
	Watt        float64
}

// =================================================================
// 0. CORE LOGIC (Reuse Client & Timeout Panjang 30s)
// =================================================================
func syncAndNotify(app *firebase.App, firestoreClient *firestore.Client, data SyncData) (status string, err error) {
    // Gunakan timeout panjang
    ctxWrite, cancel := context.WithTimeout(context.Background(), 45*time.Second)
    defer cancel()

	docID := fmt.Sprintf("user%d_%s", data.UserID, strings.ReplaceAll(data.DeviceLabel, " ", "_"))
	docRef := firestoreClient.Collection("monitoring_live").Doc(docID)

	statusDevice := "ON"
	if data.Watt < 0.1 || data.Ampere < 0.01 || data.Voltase < 1.0 {
		statusDevice = "OFF"
	}

	var previousStatus string = "UNKNOWN"
    // Operasi Baca (GET)
	snap, err := docRef.Get(ctxWrite)
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
			sendNotification(context.Background(), app, userToken, notifTitle, notifBody)
		}
	}

    // Operasi Tulis (SET)
	_, err = docRef.Set(ctxWrite, map[string]interface{}{
		"user_id":     data.UserID,
		"device_name": data.DeviceLabel,
		"voltase":     data.Voltase,
		"ampere":      data.Ampere,
		"watt":        data.Watt,
		"status":      statusDevice,
		"last_update": firestore.ServerTimestamp,
	}, firestore.MergeAll)

	if err != nil {
		log.Printf("❌ [CORE] Gagal update Firestore User %d: %v", data.UserID, err)
		return statusDevice, err
	}

	log.Printf("✅ [CORE] Sync Sukses User %d: %s | Status: %s", data.UserID, docID, statusDevice)
	return statusDevice, nil
}

// =================================================================
// 1. IOT HANDLERS (HTTP)
// =================================================================

func IotInputHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var data IotData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		return
	}

    // Client ad-hoc untuk HTTP request
    client, err := app.Firestore(r.Context())
    if err != nil {
        http.Error(w, "Firestore Init Error", http.StatusInternalServerError)
        return
    }
    defer client.Close()

	status, err := syncAndNotify(app, client, SyncData{
		UserID:      data.UserID,
		DeviceLabel: data.DeviceLabel,
		Voltase:     data.Voltase,
		Ampere:      data.Ampere,
		Watt:        data.Watt,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "device_status": status})
}

func GetCommandForDeviceHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	json.NewEncoder(w).Encode(CommandResponse{Status: "success", Command: "NONE"})
}

func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := context.Background()
	query := r.URL.Query()
	userIDStr := query.Get("user_id")
	deviceLabel := query.Get("device_label")
	if deviceLabel == "" { deviceLabel = "Sensor Utama" }
	
	targetID := 16 
	if userIDStr != "" {
		if id, err := strconv.Atoi(userIDStr); err == nil {
			targetID = id
		}
	}

	clientRest := http.Client{Timeout: 10 * time.Second}
	resp, err := clientRest.Get(RTDB_REST_URL)
	if err != nil {
		http.Error(w, "RTDB Error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	
	var rtdbData RtdbSensorData
	json.NewDecoder(resp.Body).Decode(&rtdbData)

    fsClient, err := app.Firestore(ctx)
    if err != nil {
        http.Error(w, "Firestore Error", http.StatusInternalServerError)
        return
    }
    defer fsClient.Close()

	status, _ := syncAndNotify(app, fsClient, SyncData{
		UserID:      targetID,
		DeviceLabel: deviceLabel,
		Voltase:     rtdbData.Voltage,
		Ampere:      rtdbData.Current,
		Watt:        rtdbData.Power,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "device_status": status})
}


// =================================================================
// 2. SCHEDULER INTERNAL (GABUNGAN: HARDCODE + OPTIMIZED REUSE)
// =================================================================

// [PERUBAHAN PENTING] Menerima parameter targetUserID lagi (Gaya Lama)
// Tapi di dalamnya pakai logika Reuse Client (Gaya Baru)
func StartInternalScheduler(app *firebase.App, targetUserID int, deviceLabel string, interval time.Duration) {
	go func() {
        // 1. [OPTIMISASI BARU] Buat Client Firestore SEKALI SAJA
        ctxBg := context.Background()
        fsClient, err := app.Firestore(ctxBg)
        if err != nil {
            log.Printf("❌ [SCHEDULER FATAL] Gagal init Firestore Client global: %v", err)
            return 
        }
        defer fsClient.Close() 

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("⏰ Scheduler Internal dimulai untuk User %d, sync setiap %v...", targetUserID, interval)

		for {
			select {
			case <-ticker.C:
                // 2. Ambil data RTDB
				client := http.Client{Timeout: 10 * time.Second}
				resp, err := client.Get(RTDB_REST_URL)
				if err != nil {
					log.Printf("❌ [SCHEDULER] Gagal HTTP GET ke RTDB: %v", err)
					continue
				}
				
				bodyBytes, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil { continue }

				var rtdbData RtdbSensorData
				if err := json.Unmarshal(bodyBytes, &rtdbData); err != nil {
					log.Printf("❌ [SCHEDULER] Gagal parsing JSON RTDB: %v", err)
					continue
				}

                // 3. [LOGIKA LAMA] Pakai Target User ID yang dikirim dari main.go
                // 4. [OPTIMISASI BARU] Pakai fsClient yang sudah di-reuse dan fungsi syncAndNotify baru
                
                // Gunakan goroutine agar scheduler tidak terblokir menunggu ini selesai
                go func() {
                    syncAndNotify(app, fsClient, SyncData{
                        UserID:      targetUserID, 
                        DeviceLabel: deviceLabel, 
                        Voltase:     rtdbData.Voltage,
                        Ampere:      rtdbData.Current,
                        Watt:        rtdbData.Power,
                    })
                }()
			}
		}
	}()
}

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
    }
    
    ctxSend, cancel := context.WithTimeout(ctx, 15*time.Second) 
    defer cancel()
    
    _, err = client.Send(ctxSend, msg)
    if err != nil {
        log.Printf("❌ Gagal kirim notif: %v", err)
    }
}