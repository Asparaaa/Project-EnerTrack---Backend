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

	sqldb "EnerTrack-BE/db" // Import database SQL

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	"firebase.google.com/go/messaging"
)

// --- KONFIGURASI REST API ---
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
// 0. CORE LOGIC (SAMA SEPERTI SEBELUMNYA)
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

// ... (Handlers HTTP IotInputHandler, GetCommandForDeviceHandler, RealtimeDBToFirestoreHandler tetap sama) ...
// Saya skip agar tidak terlalu panjang, fungsinya tetap sama.
// Untuk kode lengkap, gabungkan dengan bagian handlers HTTP yang lama.

func IotInputHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
    // ... (Kode HTTP Handler sama seperti sebelumnya) ...
    // Pastikan copy-paste dari file sebelumnya jika tidak berubah
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed, use POST", http.StatusMethodNotAllowed)
		return
	}
    // ... Implementasi sama ...
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

func GetCommandForDeviceHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
    // ... (Kode HTTP Handler sama) ...
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

func RealtimeDBToFirestoreHandler(w http.ResponseWriter, r *http.Request, app *firebase.App) {
    // ... (Kode HTTP Handler sama) ...
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
// 4. SCHEDULER INTERNAL (DINAMIS)
// =================================================================

// Fungsi baru untuk mengambil semua user aktif yang punya device IoT
// Untuk MVP (Minimum Viable Product), kita bisa ambil user terakhir yang login,
// atau membuat tabel khusus 'iot_mappings'. 
// Untuk sekarang, kita akan mensimulasikan mengambil list user yang terdaftar.
func getAllActiveIoTUsers() ([]int, error) {
    // Query ke Database SQL kamu: "SELECT user_id FROM users" (atau tabel khusus device)
    // Contoh sederhana: Mengambil semua user ID yang ada token FCM-nya (asumsi user aktif)
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
    
    // Fallback jika DB kosong saat dev, kembalikan ID defaultmu
    if len(userIDs) == 0 {
        return []int{16}, nil // Default ke User ID kamu
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
                log.Println("--- Memicu Sinkronisasi Dinamis ---")
                
                // 1. Ambil Data RTDB (Hanya sekali karena cuma 1 alat fisik saat ini)
                client := http.Client{Timeout: 10 * time.Second}
                resp, err := client.Get(RTDB_REST_URL)
                if err != nil {
                    log.Printf("❌ [SCHEDULER] Gagal GET RTDB: %v", err)
                    continue
                }
                
                bodyBytes, _ := io.ReadAll(resp.Body)
                resp.Body.Close()
                
                var rtdbData RtdbSensorData
                if err := json.Unmarshal(bodyBytes, &rtdbData); err != nil {
                    log.Printf("❌ [SCHEDULER] Gagal parse JSON: %v", err)
                    continue
                }

                // 2. Ambil Daftar User yang harus menerima update ini
                // (Karena alatnya cuma 1, kita akan 'broadcast' update ini ke user yang relevan)
                // Di dunia nyata, data RTDB harus punya field "owner_id".
                // TAPI, karena belum ada, kita ambil dari DB SQL secara dinamis.
                activeUsers, err := getAllActiveIoTUsers()
                if err != nil {
                    log.Printf("⚠️ Gagal ambil user dari DB, pakai default.")
                    activeUsers = []int{16} // Fallback
                }

                // 3. Loop update untuk setiap user aktif (Simulasi Multi-User)
                ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
                for _, uid := range activeUsers {
                    // Update Firestore untuk user ini
                    // Kita asumsikan semua user ini memantau "Sensor Utama" yang sama
                    syncAndNotify(ctx, app, SyncData{
                        UserID:      uid, 
                        DeviceLabel: "Sensor Utama", // Bisa juga diambil dari DB per user
                        Voltase:     rtdbData.Voltage,
                        Ampere:      rtdbData.Current,
                        Watt:        rtdbData.Power,
                    })
                }
                cancel()
            }
        }
    }()
}

// Fungsi Bantu (Sama)
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
	response, err := client.Send(ctx, msg)
	if err != nil {
		log.Printf("❌ Gagal kirim notif: %v", err)
	} else {
		log.Printf("✅ Notification sent to %s... ID: %s", token[:10], response)
	}
}