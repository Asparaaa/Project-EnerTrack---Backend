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

	"EnerTrack-BE/db"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	_ "firebase.google.com/go/db"
	"firebase.google.com/go/messaging"
)

// URL REST API RTDB
const RTDB_REST_URL = "https://enertrack-test-default-rtdb.asia-southeast1.firebasedatabase.app/sensor.json"

// ID User Default (Masih hardcode untuk simulasi, nanti bisa dinamis)
const SYNC_USER_ID = 16

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
// 1. IOT INPUT HANDLER (POST)
// =================================================================
func IotInputHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data IotData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		log.Printf("⚠️ Format JSON salah")
		return
	}

	processDataToFirestore(w, app, SyncData{
		UserID:      data.UserID,
		DeviceLabel: data.DeviceLabel,
		Voltase:     data.Voltase,
		Ampere:      data.Ampere,
		Watt:        data.Watt,
	})
}

// =================================================================
// 2. GET COMMAND HANDLER
// =================================================================
func GetCommandForDeviceHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	json.NewEncoder(w).Encode(CommandResponse{Status: "success", Command: "NONE"})
}

// =================================================================
// 3. REALTIME DB TO FIRESTORE HANDLER (SYNC)
// =================================================================
func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query()
	deviceLabel := query.Get("device_label")
	if deviceLabel == "" {
		deviceLabel = "Default Meter"
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(RTDB_REST_URL)
	if err != nil {
		http.Error(w, "Error fetching RTDB", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var rtdbData RtdbSensorData
	json.Unmarshal(bodyBytes, &rtdbData)

	syncData := SyncData{
		UserID:      SYNC_USER_ID,
		DeviceLabel: deviceLabel,
		Voltase:     rtdbData.Voltage,
		Ampere:      rtdbData.Current,
		Watt:        rtdbData.Power,
	}

	processDataToFirestore(w, app, syncData)
}

// --- LOGIKA UTAMA UPDATE & NOTIFIKASI ---
func processDataToFirestore(w http.ResponseWriter, app *firebase.App, data SyncData) {
	ctx := context.Background()
	firestoreClient, err := app.Firestore(ctx)
	if err != nil {
		http.Error(w, "Firestore Error", http.StatusInternalServerError)
		return
	}
	defer firestoreClient.Close()

	docID := fmt.Sprintf("user%d_%s", data.UserID, strings.ReplaceAll(data.DeviceLabel, " ", "_"))
	docRef := firestoreClient.Collection("monitoring_live").Doc(docID)

	// Status Baru
	statusDevice := "ON"
	if data.Watt == 0 {
		statusDevice = "OFF"
	}

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
	// Ambil token dari DB hanya jika perlu kirim notifikasi
	var userToken string
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
	// Kita pakai logika ini biar gak nyepam notif kalau alatnya mati terus
	if previousStatus == "ON" && statusDevice == "OFF" {
		shouldNotify = true
        notifTitle = "Device Turned OFF"
        notifBody = fmt.Sprintf("Device %s is now inactive (0 Watt).", data.DeviceLabel)
	}

    // Kondisi 3: Perangkat Nyala (Transisi dari OFF ke ON) - REQUEST BARU
    if (previousStatus == "OFF" || previousStatus == "UNKNOWN") && statusDevice == "ON" {
        shouldNotify = true
        notifTitle = "Device Turned ON"
        notifBody = fmt.Sprintf("Device %s is now active and consuming power.", data.DeviceLabel)
    }

	if shouldNotify {
		userToken = getUserFcmTokenFromDB(data.UserID)
		if userToken != "" {
			log.Printf("🔔 Sending Notification: %s", notifTitle)
			sendNotification(ctx, app, userToken, notifTitle, notifBody)
		} else {
			log.Printf("❌ Token not found for User %d", data.UserID)
		}
	}
	// -----------------------------------

	// Tulis ke Firestore (Tanpa kwh_total)
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
		http.Error(w, "Firestore Write Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Data synced & Checked for Alert",
		"device":  data.DeviceLabel,
		"power":   data.Watt,
	})
}

// Fungsi Bantu: Ambil Token dari MySQL
func getUserFcmTokenFromDB(userID int) string {
	var token string
	query := "SELECT fcm_token FROM users WHERE user_id = ?"
	err := db.DB.QueryRow(query, userID).Scan(&token)
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