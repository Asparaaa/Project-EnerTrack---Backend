package handlers

import (
	"EnerTrack-BE/db"
	"EnerTrack-BE/models" // Pastikan import ini sesuai dengan struktur project kamu
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"github.com/google/generative-ai-go/genai"
)

// Definisikan Request Body untuk endpoint /analyze yang baru
type AnalyzeRequest struct {
	Devices      []models.Device `json:"devices"`
	BesarListrik string          `json:"besar_listrik"`
}

// ==================================================================================
// 1. ANALYZE HANDLER (Menerima input perangkat, hitung, tanya AI, simpan DB)
// ==================================================================================
func AnalyzeHandler(w http.ResponseWriter, r *http.Request, model *genai.GenerativeModel) {
	log.Println("🔹 /analyze endpoint dipanggil")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Cek Session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		http.Error(w, "Session not found", http.StatusUnauthorized)
		return
	}
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, "User ID not found", http.StatusUnauthorized)
		return
	}

	// Decode JSON Body
	var req AnalyzeRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid data", http.StatusBadRequest)
		return
	}

	if len(req.Devices) == 0 {
		http.Error(w, "No devices selected", http.StatusBadRequest)
		return
	}

	// Kalkulasi Power
	totalDailyWattHours := 0
	for _, device := range req.Devices {
		totalDailyWattHours += device.Power * device.Duration
	}

	dailyKWh := float64(totalDailyWattHours) / 1000.0
	monthlyKWh := dailyKWh * 30
	tariffRate := getTariffRate(req.BesarListrik)
	estimatedMonthlyCost := monthlyKWh * tariffRate

	// AI Process
	prompt := buildPrompt(req.Devices)
	ctx := r.Context()
	sessionAI := model.StartChat()
	resp, err := sessionAI.SendMessage(ctx, genai.Text(prompt))
	if err != nil {
		http.Error(w, "Failed to get AI response", http.StatusInternalServerError)
		return
	}
	aiResponse := formatAIResponse(resp)

	// Cek Riwayat Terakhir (Optional)
	var idSubmit string
	var riwayatID int
	err = db.DB.QueryRow(`SELECT id_submit, id FROM riwayat_perangkat WHERE user_id = ? ORDER BY tanggal_input DESC, id DESC LIMIT 1`, userID).Scan(&idSubmit, &riwayatID)
	if err != nil {
		riwayatID = 0 // Kalau tidak ada riwayat, set 0
	}

	// Simpan Hasil Analisis ke DB
	err = SaveAnalysisToDB(userID, riwayatID, totalDailyWattHours, dailyKWh, aiResponse, estimatedMonthlyCost)
	if err != nil {
		log.Printf("❌ Failed to save analysis: %v", err)
	}

	// Response ke Frontend
	response := map[string]interface{}{
		"total_power_wh":       totalDailyWattHours,
		"daily_kwh":            dailyKWh,
		"monthly_kwh":          monthlyKWh,
		"tariff_rate":          tariffRate,
		"estimated_monthly_rp": formatRupiah(estimatedMonthlyCost),
		"ai_response":          aiResponse,
		"id_submit":            idSubmit,
		"besar_listrik":        req.BesarListrik,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ==================================================================================
// 2. FUNGSI SAVE TO DB
// ==================================================================================
func SaveAnalysisToDB(userID int, riwayatID int, totalPowerWh int, totalPowerKWh float64, aiResponse string, estimatedCost float64) error {
	estimatedCostRp := formatRupiah(estimatedCost)

	// Pastikan tabel 'hasil_analisis' sudah ada di database kamu!
	query := `
        INSERT INTO hasil_analisis (
            user_id,
            riwayat_id,
            total_power_wh,
            total_power_kwh,
            ai_response,
            estimated_cost_rp
        ) VALUES (?, ?, ?, ?, ?, ?)`

	_, err := db.DB.Exec(query,
		userID,
		riwayatID,
		totalPowerWh,
		totalPowerKWh,
		aiResponse,
		estimatedCostRp,
	)

	if err != nil {
		return fmt.Errorf("failed to save analysis: %v", err)
	}

	log.Printf("✅ Analysis saved: UserID=%d, KWh=%.2f", userID, totalPowerKWh)
	return nil
}

// ==================================================================================
// 3. GET INSIGHT HANDLER (Grade Efisiensi - ENGLISH VERSION)
// ==================================================================================

type Tip struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	IconType    string `json:"icon_type"`
}

type InsightResponse struct {
	Grade       string `json:"grade"`
	Message     string `json:"message"`
	Percentage  int    `json:"percentage"`
	Tips        []Tip  `json:"tips"`
	Calculation string `json:"calculation_basis"` // Tambahan info buat frontend
}

func GetInsightHandler(w http.ResponseWriter, r *http.Request) {
	// A. Autentikasi User
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID, ok := session.Values["user_id"].(int)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// B. Ambil Data Analisis Terakhir (Daily KWh)
	var lastDailyKwh float64
	queryLastAnalysis := `
		SELECT total_power_kwh 
		FROM hasil_analisis 
		WHERE user_id = ? 
		ORDER BY id DESC LIMIT 1
	`
	err = db.DB.QueryRow(queryLastAnalysis, userID).Scan(&lastDailyKwh)
	if err != nil {
		lastDailyKwh = 0 // Belum pernah analisis
	}

	// Konversi ke Bulanan (x30 hari)
	estimatedMonthlyKwh := lastDailyKwh * 30

	// C. Ambil Kapasitas Listrik Rumah (VA)
	var capacityStr string
	queryCap := `SELECT besar_listrik FROM riwayat_perangkat WHERE user_id = ? ORDER BY id DESC LIMIT 1`
	err = db.DB.QueryRow(queryCap, userID).Scan(&capacityStr)
	
	capacity := 1300.0 // Default
	if err == nil {
		cleanCap := strings.ReplaceAll(strings.ReplaceAll(capacityStr, " VA", ""), ".", "")
		fmt.Sscanf(cleanCap, "%f", &capacity)
	}
	if capacity == 0 { capacity = 1300 }

	// D. Logic Perhitungan Grade
	maxKwhReasonable := (capacity / 1000.0) * 24 * 30 * 0.4

	var usagePercentage int
	if maxKwhReasonable > 0 {
		usagePercentage = int((estimatedMonthlyKwh / maxKwhReasonable) * 100)
	} else {
		usagePercentage = 0
	}

	var grade, message string
	var tips []Tip

	// Logic Grading (ENGLISH)
	switch {
	case usagePercentage < 80:
		grade = "A"
		message = "Excellent! Very energy efficient."
		tips = []Tip{
			{"Keep it up", "Your usage patterns are optimal.", "general"},
			{"Check Standby Power", "Ensure no 'vampire power' consumption.", "plug"},
		}
	case usagePercentage <= 120:
		grade = "B"
		message = "Good! Usage is within reasonable limits."
		tips = []Tip{
			{"Optimize AC", "Set AC temperature to 24-25°C.", "ac"},
			{"Use Natural Light", "Open windows during the day.", "lamp"},
		}
	default:
		grade = "C"
		message = "Attention! Usage is quite high."
		tips = []Tip{
			{"Use Timers", "Limit duration of AC/TV usage.", "ac"},
			{"Unplug Devices", "Unplug electronics when not in use.", "plug"},
		}
	}

	// Kirim JSON
	response := InsightResponse{
		Grade:       grade,
		Message:     message,
		Percentage:  usagePercentage,
		Tips:        tips,
		Calculation: "monthly_projection",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ==================================================================================
// 4. HELPER FUNCTIONS
// ==================================================================================

func formatRupiah(amount float64) string {
	rounded := math.Floor(amount)
	amountStr := fmt.Sprintf("%.0f", rounded)
	var result strings.Builder
	length := len(amountStr)
	for i, ch := range amountStr {
		if i > 0 && (length-i)%3 == 0 {
			result.WriteRune('.')
		}
		result.WriteRune(ch)
	}
	return "Rp. " + result.String()
}

func getTariffRate(besarListrik string) float64 {
	switch {
	case strings.Contains(besarListrik, "900"):
		return 1352
	case strings.Contains(besarListrik, "1300"), strings.Contains(besarListrik, "2200"):
		return 1444.70
	case strings.Contains(besarListrik, "3500"), strings.Contains(besarListrik, "4400"), strings.Contains(besarListrik, "5500"):
		return 1699.53
	case strings.Contains(besarListrik, "6600"), strings.Contains(besarListrik, "7700"), strings.Contains(besarListrik, "9000"):
		return 1699.53
	default:
		return 1500
	}
}

func buildPrompt(devices []models.Device) string {
	var sb strings.Builder
	sb.WriteString("### Electricity Usage Analysis\n\n")
	if len(devices) > 0 {
		sb.WriteString(fmt.Sprintf("- Power Capacity: %s\n", devices[0].Besar_Listrik))
	}
	sb.WriteString("### Devices:\n")
	for _, device := range devices {
		sb.WriteString(fmt.Sprintf("- %s (%d Watts), %d hours/day\n", device.Name, device.Power, device.Duration))
	}
	sb.WriteString("\nResponse format:\n- Bullet points only.\n- Convert to kWh.\n- Suggestion.\n")
	return sb.String()
}

func formatAIResponse(resp *genai.GenerateContentResponse) string {
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "No AI response received."
	}
	var aiResponse strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if text, ok := part.(genai.Text); ok {
			cleanText := strings.ReplaceAll(string(text), "*", "")
			aiResponse.WriteString(cleanText + "\n")
		}
	}
	result := strings.TrimSpace(aiResponse.String())
	return strings.ReplaceAll(result, "\n\n", "\n")
}