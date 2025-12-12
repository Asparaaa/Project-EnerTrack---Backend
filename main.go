package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"EnerTrack-BE/db"
	"EnerTrack-BE/handlers"

	firebase "firebase.google.com/go"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// ... (Struct UserInput & Device biarkan saja) ...
type UserInput struct {
	BillingType string `json:"billingtype"`
	Electricity struct {
		Amount float64 `json:"amount,omitempty"`
		Kwh    float64 `json:"kwh,omitempty"`
	} `json:"electricity"`
	Devices []Device `json:"devices"`
}

type Device struct {
	Name             string `json:"name"`
	Brand            string `json:"brand"`
	Power            int    `json:"power"`
	Duration         int    `json:"duration"`
	Jenis_Pembayaran string `json:"jenis_pembayaran"`
	Besar_Listrik    string `json:"besar_listrik"`
	Weekly_Usage     int    `json:"weekly_usage"`
	Monthly_Usage    int    `json:"monthly_usage"`
	Monthly_Cost     int    `json:"monthly_cost"`
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

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
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

	app, err := firebase.NewApp(ctx, nil, sa)
	if err != nil {
		log.Printf("❌ Gagal init Firebase App: %v", err)
	}
	
	// Firestore Client hanya untuk pengecekan koneksi awal di main
	firestoreClientDB, err := app.Firestore(ctx)
	if err != nil {
		log.Printf("❌ Gagal konek Firestore: %v", err)
	} else {
		defer firestoreClientDB.Close()
	}

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
	router.HandleFunc("/analyze", func(w http.ResponseWriter, r *http.Request) {
		handlers.AnalyzeHandler(w, r, model)
	})
	router.HandleFunc("/api/insight", handlers.GetInsightHandler)
	router.HandleFunc("/api/devices", handlers.GetDevicesByBrandHandler)
	router.HandleFunc("/house-capacity", handlers.GetHouseCapacityHandler)
	router.HandleFunc("/api/devices/list", handlers.GetUniqueDevicesHandler)
	router.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		handlers.ChatHandler(w, r, model)
	})
	router.HandleFunc("/user/appliances", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlers.GetUserAppliancesHandler(w, r)
		case http.MethodPost:
			handlers.CreateApplianceHandler(w, r)
		case http.MethodPut:
			handlers.UpdateApplianceHandler(w, r)
		case http.MethodDelete:
			handlers.DeleteApplianceHandler(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	router.HandleFunc("/user/appliances/", handlers.GetApplianceByIDHandler)
	router.HandleFunc("/user/profile", handlers.UpdateUserProfileHandler)

	// === ROUTE BARU BUAT UPDATE TOKEN ===
	router.HandleFunc("/user/fcm-token", handlers.UpdateFcmTokenHandler)
	// ====================================

	// Route IoT Input
	router.HandleFunc("/api/iot/input", func(w http.ResponseWriter, r *http.Request) {
		handlers.IotInputHandler(w, r, app)
	})

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