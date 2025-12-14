package main

import (
	"context"
	"encoding/json" // Wajib ada untuk encode JSON
	"log"
	"net/http"
	"os"
	"time"

	"EnerTrack-BE/db"
	"EnerTrack-BE/handlers"

	firebase "firebase.google.com/go"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// --- STRUCT DATA UNTUK APPLIANCES ---
type ApplianceSimple struct {
	ID       int     `json:"id"`
	UserID   int     `json:"user_id"`
	Name     string  `json:"name"`
	Brand    string  `json:"brand"`
	Power    int     `json:"power"`
	Duration float64 `json:"duration"`
}

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

// [BARU] Handler Asli: Mengambil data appliances dari Database
func RealUserAppliancesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: Di production, ambil UserID dari context/session.
	// Untuk sekarang kita hardcode ke 16 agar fitur AI jalan di demo ini.
	targetUserID := 16 

	// Query ke Database
	rows, err := db.DB.Query("SELECT id, user_id, name, brand, power, duration FROM appliances WHERE user_id = ?", targetUserID)
	if err != nil {
		log.Printf("❌ Gagal query appliances: %v", err)
		// Jangan panik, kembalikan array kosong jika error DB agar app tidak crash
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]")) 
		return
	}
	defer rows.Close()

	var appliances []ApplianceSimple
	for rows.Next() {
		var a ApplianceSimple
		// Pastikan urutan Scan sesuai dengan urutan SELECT
		if err := rows.Scan(&a.ID, &a.UserID, &a.Name, &a.Brand, &a.Power, &a.Duration); err == nil {
			appliances = append(appliances, a)
		} else {
			log.Printf("⚠️ Gagal scan baris: %v", err)
		}
	}

	// Return JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	
	if len(appliances) == 0 {
		w.Write([]byte("[]")) // Pastikan return [] jika kosong, bukan null
	} else {
		json.NewEncoder(w).Encode(appliances)
	}
	
	log.Printf("✅ Served %d appliances for User %d (AI Insight Ready)", len(appliances), targetUserID)
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

	conf := &firebase.Config{
		DatabaseURL: "https://enertrack-test-default-rtdb.asia-southeast1.firebasedatabase.app",
		ProjectID:   "enertrack-test",
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
	// 🔥 PENTING: MENJALANKAN SCHEDULER INTERNAL (DINAMIS)
	// =================================================================
	const syncInterval = 2 * time.Second
	if app != nil {
		handlers.StartInternalScheduler(app, syncInterval)
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

	// === ROUTE AI ANALYZE (SUDAH ADA) ===
	router.HandleFunc("/analyze", func(w http.ResponseWriter, r *http.Request) {
		handlers.AnalyzeHandler(w, r, model)
	})

	// === [PERBAIKAN] ROUTE AI CHAT (INI YANG KEMAREN HILANG!) ===
	// Kita daftarkan di "/chat" agar sesuai dengan ApiService Android
	router.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
		handlers.ChatHandler(w, r, model)
	})

	router.HandleFunc("/api/insight", handlers.GetInsightHandler)
	router.HandleFunc("/api/devices", handlers.GetDevicesByBrandHandler)
	router.HandleFunc("/house-capacity", handlers.GetHouseCapacityHandler)
	router.HandleFunc("/api/devices/list", handlers.GetUniqueDevicesHandler)

    // [PERBAIKAN] Menggunakan RealUserAppliancesHandler agar AI bisa baca data
	router.HandleFunc("/user/appliances", RealUserAppliancesHandler)
	
	router.HandleFunc("/user/appliances/", handlers.GetApplianceByIDHandler)
	router.HandleFunc("/user/profile", handlers.UpdateUserProfileHandler)

	router.HandleFunc("/api/iot/input", func(w http.ResponseWriter, r *http.Request) {
		handlers.IotInputHandler(w, r, app)
	})

	router.HandleFunc("/api/iot/command", func(w http.ResponseWriter, r *http.Request) {
		handlers.GetCommandForDeviceHandler(w, r, app)
	})

	router.HandleFunc("/api/rtdb/sync", func(w http.ResponseWriter, r *http.Request) {
		handlers.RealtimeDBToFirestoreHandler(w, r, app)
	})

	router.HandleFunc("/update-token", handlers.UpdateFcmTokenHandler)
	router.HandleFunc("/update-token/", handlers.UpdateFcmTokenHandler)

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