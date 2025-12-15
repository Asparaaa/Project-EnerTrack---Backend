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
// 0. CORE LOGIC: Sinkronisasi & Notifikasi
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
	status, err := syncAndNotify(r.Context(), app, SyncData{
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
	
	// Manual Trigger via HTTP masih bisa pakai query param
	query := r.URL.Query()
	userIDStr := query.Get("user_id")
	deviceLabel := query.Get("device_label")
	if deviceLabel == "" { deviceLabel = "Sensor Utama" }
	
	targetID := 16 // Default fallback
	if userIDStr != "" {
		if id, err := strconv.Atoi(userIDStr); err == nil {
			targetID = id
		}
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(RTDB_REST_URL)
	if err != nil {
		http.Error(w, "RTDB Error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	
	var rtdbData RtdbSensorData
	json.NewDecoder(resp.Body).Decode(&rtdbData)

	status, _ := syncAndNotify(ctx, app, SyncData{
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
// 2. SCHEDULER INTERNAL (MODIFIKASI: KEMBALI KE HARDCODE PARAMETER)
// =================================================================

// Kita tambahkan parameter userID dan deviceLabel lagi supaya main.go bisa maksa User 16
func StartInternalScheduler(app *firebase.App, targetUserID int, deviceLabel string, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("⏰ Scheduler Internal dimulai untuk User %d, sync setiap %v...", targetUserID, interval)

		for {
			select {
			case <-ticker.C:
				// log.Println("--- Memicu Sinkronisasi Terjadwal (Hardcoded User) ---")
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)

				// 1. HTTP GET ke RTDB (REST API - Universal)
				client := http.Client{Timeout: 10 * time.Second}
				resp, err := client.Get(RTDB_REST_URL)

				if err != nil {
					log.Printf("❌ [SCHEDULER] Gagal HTTP GET ke RTDB: %v", err)
					cancel()
					continue
				}
				
				// Baca Body
				bodyBytes, err := io.ReadAll(resp.Body)
				resp.Body.Close() // Close immediately after read
				
				if err != nil {
					cancel()
					continue
				}

				var rtdbData RtdbSensorData
				if err := json.Unmarshal(bodyBytes, &rtdbData); err != nil {
					log.Printf("❌ [SCHEDULER] Gagal parsing JSON RTDB: %v", err)
					cancel()
					continue
				}

				// 2. Panggil fungsi inti untuk User ID yang di-HARDCODE (User 16)
				syncAndNotify(ctx, app, SyncData{
					UserID:      targetUserID, // Pakai ID yang dikirim dari main.go (16)
					DeviceLabel: deviceLabel,  // Pakai nama device dari main.go ("Sensor Utama")
					Voltase:     rtdbData.Voltage,
					Ampere:      rtdbData.Current,
					Watt:        rtdbData.Power,
				})

				cancel() // Bebaskan context
			}
		}
	}()
}

// Fungsi Bantu
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
	if err == nil {
		msg := &messaging.Message{
			Token: token,
			Notification: &messaging.Notification{
				Title: title,
				Body:  body,
			},
		}
		client.Send(ctx, msg)
	}
}