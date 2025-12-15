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
	"sync"
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
// 0. CORE LOGIC
// =================================================================
func syncAndNotify(parentCtx context.Context, app *firebase.App, data SyncData) (status string, err error) {
    // [PERBAIKAN KRITIS] Gunakan context.Background() sebagai basis untuk Firestore
    // Ini memutus ketergantungan deadline dari parentCtx (scheduler/http)
    // Jadi Firestore punya waktu penuh 30 detik, tidak peduli sisa waktu parent berapa.
    ctxFirestore, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

	firestoreClient, err := app.Firestore(ctxFirestore)
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
	snap, err := docRef.Get(ctxFirestore)
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
            // Gunakan context background untuk notif juga
			sendNotification(context.Background(), app, userToken, notifTitle, notifBody)
		}
	}

	_, err = docRef.Set(ctxFirestore, map[string]interface{}{
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
// 2. SCHEDULER INTERNAL (DINAMIS & PARALLEL)
// =================================================================

func getAllActiveIoTUsers() ([]int, error) {
    rows, err := sqldb.DB.Query("SELECT user_id FROM users WHERE fcm_token IS NOT NULL")
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var userIDs []int
    for rows.Next() {
        var id int
        if err := rows.Scan(&id); err == nil {
            userIDs = append(userIDs, id)
        }
    }
    
    if len(userIDs) == 0 {
        return []int{16}, nil 
    }
    
    return userIDs, nil
}

func StartInternalScheduler(app *firebase.App, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("⏰ Scheduler Internal (DINAMIS) dimulai, sync setiap %v...", interval)

		for {
			select {
			case <-ticker.C:
				// log.Println("--- Memicu Sinkronisasi Dinamis ---")
                
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

                activeUsers, err := getAllActiveIoTUsers()
                if err != nil {
                    log.Printf("⚠️ Gagal ambil user list, fallback ke user 16: %v", err)
                    activeUsers = []int{16}
                }

                var wg sync.WaitGroup
                
                for _, uid := range activeUsers {
                    wg.Add(1)
                    go func(targetUID int) {
                        defer wg.Done()
                        // Menggunakan context.Background() sebagai parent karena ini scheduler
                        syncAndNotify(context.Background(), app, SyncData{
                            UserID:      targetUID, 
                            DeviceLabel: "Sensor Utama", 
                            Voltase:     rtdbData.Voltage,
                            Ampere:      rtdbData.Current,
                            Watt:        rtdbData.Power,
                        })
                    }(uid)
                }
                
                wg.Wait()
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