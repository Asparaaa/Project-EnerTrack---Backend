package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"EnerTrack-BE/db"
	"EnerTrack-BE/handlers" // Pastikan package handlers diimpor

	firebase "firebase.google.com/go"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// Definisikan struct di main.go jika diperlukan, atau pastikan import handler sudah benar.

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		log.Printf("Incoming Request: %s %s", r.Method, r.URL.Path)

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Handler sementara untuk /user/appliances yang menjamin respons JSON array kosong
func TemporaryUserAppliancesHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        w.WriteHeader(http.StatusMethodNotAllowed)
        return
    }
    
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    w.Write([]byte("[]"))
    log.Println("✅ Responed to /user/appliances with empty JSON array [].")
}


func main() {
	db.InitDB()
	defer db.DB.Close()

	// --- SETUP FIREBASE ---
	firebaseCreds := os.Getenv("FIREBASE_CREDENTIALS")
	ctx := context.Background()
	var sa option.ClientOption

	if firebaseCreds != "" {
		log.Println("✅ FIREBASE_CREDENTIALS ditemukan")
		sa = option.WithCredentialsJSON([]byte(firebaseCreds))
	} else {
		log.Println("⚠️ FIREBASE_CREDENTIALS kosong, cari file lokal...")
		sa = option.WithCredentialsFile("serviceAccountKey.json")
	}

    // 🔥 PERBAIKAN: FIX URL REGIONAL RTDB (KONFIGURASI PALING DASAR)
	conf := &firebase.Config{
		// [PERBAIKAN]: Kembali ke konfigurasi dasar. URL harus ada.
        // Hapus trailing slash jika ada.
        DatabaseURL: "https://enertrack-test-default-rtdb.asia-southeast1.firebasedatabase.app",
        
        // ProjectID tetap harus ada
        ProjectID: "enertrack-test-default-rtdb", 
	}

	app, err := firebase.NewApp(ctx, conf, sa) 
	if err != nil {
		log.Printf("❌ Gagal init Firebase App: %v", err)
	}
	
	firestoreClientDB, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ Gagal konek Firestore: %v", err)
	} else {
		defer firestoreClientDB.Close()
	}
    
    // =================================================================
    // 🔥 PENTING: MENJALANKAN SCHEDULER INTERNAL DI BACKGROUND
    // =================================================================
    
    // !!! GANTI NILAI INI DENGAN USER ID DAN DEVICE LABEL YANG BENAR !!!
    const targetUserID = 16 
    const targetDevice = "Sensor Utama" 
    const syncInterval = 1 * time.Minute // Sinkronisasi setiap 1 menit (untuk testing)
    
    if app != nil {
        handlers.StartInternalScheduler(app, targetUserID, targetDevice, syncInterval)
    }
    // =================================================================

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatalln("⚠️ GEMINI_API_KEY tidak ditemukan")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatalf("Error AI client: %v", err)
	}
	defer client.Close()

	model := client.GenerativeModel("gemini-2.5-flash")

	router := http.NewServeMux()
    // --- PENDAFTARAN RUTING LAINNYA ---
    router.HandleFunc("/login", handlers.LoginHandler)
    router.HandleFunc("/register", handlers.RegisterHandler)
    router.HandleFunc("/logout", handlers.LogoutHandler)
    router.HandleFunc("/auth/check-session", handlers.CheckSessionHandler)
    router.HandleFunc("/statistics/weekly", handlers.GetWeeklyStatisticsHandler)
    router.HandleFunc("/statistics/monthly", handlers.GetMonthlyStatisticsHandler)
    router.HandleFunc("/statistics/data-range", handlers.GetDataRangeHandler)
    router.HandleFunc("/statistics/category", handlers.GetCategoryStatisticsHandler)
    router.HandleFunc("/history", handlers.GetDeviceHistoryHandler)
    router.HandleFunc("/brands", handlers.GetBrandsHandler)
    router.HandleFunc("/categories", handlers.GetCategoriesHandler)
    router.HandleFunc("/submit", handlers.SubmitHandler)
    
    // Rute AI
    router.HandleFunc("/analyze", func(w http.ResponseWriter, r *http.Request) {
        handlers.AnalyzeHandler(w, r, model) 
    })
    
    router.HandleFunc("/api/insight", handlers.GetInsightHandler)
    router.HandleFunc("/api/devices", handlers.GetDevicesByBrandHandler)
    router.HandleFunc("/house-capacity", handlers.GetHouseCapacityHandler)
    router.HandleFunc("/api/devices/list", handlers.GetUniqueDevicesHandler)
    
    // Rute Appliances & Profile
    router.HandleFunc("/user/appliances", TemporaryUserAppliancesHandler)
    router.HandleFunc("/user/appliances/", handlers.GetApplianceByIDHandler)
    router.HandleFunc("/user/profile", handlers.UpdateUserProfileHandler)
    
    // 🔥 ROUTE KHUSUS IOT 🔥
    // 1. Rute POST untuk mengirim data dari Arduino (Push)
    router.HandleFunc("/api/iot/input", func(w http.ResponseWriter, r *http.Request) {
        handlers.IotInputHandler(w, r, app)
    })
    
    // 2. Rute GET untuk Polling Perintah ke Arduino (Pull)
    router.HandleFunc("/api/iot/command", func(w http.ResponseWriter, r *http.Request) {
        handlers.GetCommandForDeviceHandler(w, r, app)
    })
    
    // 3. Rute GET untuk Sinkronisasi RTDB ke Firestore
    log.Println("⚡️ DEBUG: Mendaftarkan rute sinkronisasi RTDB ke Firestore: /api/rtdb/sync (Sekarang Opsional)")
    router.HandleFunc("/api/rtdb/sync", func(w http.ResponseWriter, r *http.Request) {
        handlers.RealtimeDBToFirestoreHandler(w, r, app)
    })
    
	// === ROUTE FCM FINAL: Menggunakan Path Root Sederhana /update-token ===
	log.Println("⚡️ DEBUG: Mendaftarkan rute FCM final: /update-token (Non-slash & Trailing Slash)")
	router.HandleFunc("/update-token", handlers.UpdateFcmTokenHandler)
	router.HandleFunc("/update-token/", handlers.UpdateFcmTokenHandler)
	// ===================================

	finalHandler := corsMiddleware(router)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	addr := "0.0.0.0:" + port
	log.Printf("✅ Server berjalan di %s", addr)
	if err := http.ListenAndServe(addr, finalHandler); err != nil {
		log.Fatal("❌ Error starting server:", err)
	}
}