package handlers

import (
	"EnerTrack-BE/db"
	"EnerTrack-BE/models"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"github.com/google/generative-ai-go/genai"
)

// Definisikan Request Body
type AnalyzeRequest struct {
	Devices      []models.Device `json:"devices"`
	BesarListrik string          `json:"besar_listrik"`
}

// ==================================================================================
// 1. ANALYZE HANDLER (Tetap dipertahankan buat fitur deep analysis)
// ==================================================================================
func AnalyzeHandler(w http.ResponseWriter, r *http.Request, model *genai.GenerativeModel) {
	// ... (Bagian ini tidak perlu diubah, biarkan seperti sebelumnya)
	log.Println("🔹 /analyze endpoint dipanggil")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	totalDailyWattHours := 0
	for _, device := range req.Devices {
		totalDailyWattHours += device.Power * device.Duration
	}

	dailyKWh := float64(totalDailyWattHours) / 1000.0
	monthlyKWh := dailyKWh * 30
	tariffRate := getTariffRate(req.BesarListrik)
	estimatedMonthlyCost := monthlyKWh * tariffRate

	prompt := buildPrompt(req.Devices)
	ctx := r.Context()
	sessionAI := model.StartChat()
	resp, err := sessionAI.SendMessage(ctx, genai.Text(prompt))
	if err != nil {
		http.Error(w, "Failed to get AI response", http.StatusInternalServerError)
		return
	}
	aiResponse := formatAIResponse(resp)

	var idSubmit string
	var riwayatID int
	err = db.DB.QueryRow(`SELECT id_submit, id FROM riwayat_perangkat WHERE user_id = ? ORDER BY tanggal_input DESC, id DESC LIMIT 1`, userID).Scan(&idSubmit, &riwayatID)
	if err != nil {
		riwayatID = 0
	}

	err = SaveAnalysisToDB(userID, riwayatID, totalDailyWattHours, dailyKWh, aiResponse, estimatedMonthlyCost)
	if err != nil {
		log.Printf("❌ Failed to save analysis: %v", err)
	}

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

func SaveAnalysisToDB(userID int, riwayatID int, totalPowerWh int, totalPowerKWh float64, aiResponse string, estimatedCost float64) error {
	estimatedCostRp := formatRupiah(estimatedCost)
	query := `
        INSERT INTO hasil_analisis (
            user_id, riwayat_id, total_power_wh, total_power_kwh, ai_response, estimated_cost_rp
        ) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := db.DB.Exec(query, userID, riwayatID, totalPowerWh, totalPowerKWh, aiResponse, estimatedCostRp)
	if err != nil {
		return fmt.Errorf("failed to save analysis: %v", err)
	}
	return nil
}

// ==================================================================================
// 3. GET INSIGHT HANDLER (VERSI REAL-TIME DARI HISTORY) 🚀
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
	Calculation string `json:"calculation_basis"`
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

	// B. Ambil Total Pemakaian HARIAN dari Tabel HISTORY (riwayat_perangkat)
	// Kita ambil SUM semua alat yang diinput di BULAN INI.
	// Jadi kalau user nambah alat di history, grade langsung berubah.
	var totalDailyWh int
	queryHistorySum := `
		SELECT COALESCE(SUM(daya * durasi), 0)
		FROM riwayat_perangkat
		WHERE user_id = ? 
		AND MONTH(tanggal_input) = MONTH(CURDATE()) 
		AND YEAR(tanggal_input) = YEAR(CURDATE())
	`
	err = db.DB.QueryRow(queryHistorySum, userID).Scan(&totalDailyWh)
	if err != nil {
		totalDailyWh = 0
	}

	// Konversi ke Proyeksi Bulanan (x 30 hari)
	// (Wh / 1000) = kWh -> dikali 30 hari
	estimatedMonthlyKwh := (float64(totalDailyWh) / 1000.0) * 30

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
	// Batas Wajar = 40% dari Kapasitas Maksimum (Full Load 24 Jam)
	maxKwhReasonable := (capacity / 1000.0) * 24 * 30 * 0.4

	var usagePercentage int
	if maxKwhReasonable > 0 {
		usagePercentage = int((estimatedMonthlyKwh / maxKwhReasonable) * 100)
	} else {
		usagePercentage = 0
	}

	var grade, message string
	var tips []Tip

	// Logic Grading
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
			{"Check High Power Devices", "Your usage is exceeding normal limits.", "general"},
			{"Limit AC Usage", "Try using a timer for your air conditioner.", "ac"},
			{"Unplug Unused Electronics", "Devices on standby still consume power.", "plug"},
		}
	}

	// Kirim JSON
	response := InsightResponse{
		Grade:       grade,
		Message:     message,
		Percentage:  usagePercentage,
		Tips:        tips,
		Calculation: "monthly_projection_history", // Penanda sumber data
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ... (Helper functions tetap sama: formatRupiah, getTariffRate, buildPrompt, formatAIResponse)
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