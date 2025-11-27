// analyze.go - Versi yang diperbarui untuk menerima POST request dan daftar perangkat
package handlers

import (
	"EnerTrack-BE/db"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"EnerTrack-BE/models" // Pastikan models.Device memiliki struktur yang sesuai

	"github.com/google/generative-ai-go/genai"
)

// Definisikan Request Body untuk endpoint /analyze yang baru
type AnalyzeRequest struct {
	Devices      []models.Device `json:"devices"`
	BesarListrik string          `json:"besar_listrik"` // Tambahkan ini agar frontend bisa mengirimkan kapasitas rumah yang terpilih
}

// AnalyzeHandler menangani endpoint /analyze
func AnalyzeHandler(w http.ResponseWriter, r *http.Request, model *genai.GenerativeModel) {
	log.Println("🔹 /analyze endpoint dipanggil")

	// ✅ PERUBAHAN UTAMA: Sekarang menerima metode POST
	// Jika r.Method BUKAN POST, maka kembalikan error.
	if r.Method != http.MethodPost {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		log.Println("❌ Metode HTTP tidak diizinkan (hanya POST yang diterima)")
		return
	} else {
		log.Println("✅ Metode HTTP POST diterima")
	}

	// Ambil session
	session, err := Store.Get(r, "elektronik_rumah_session")
	if err != nil {
		log.Printf("❌ Error mendapatkan session: %v", err)
		http.Error(w, "Sesi tidak ditemukan", http.StatusUnauthorized)
		return
	}

	// Cek email di session (digunakan untuk mendapatkan userID)
	email, ok := session.Values["email"].(string)
	if !ok {
		log.Println("❌ email tidak ditemukan di sesi")
		http.Error(w, "email tidak ditemukan", http.StatusUnauthorized)
		return
	} else {
		log.Printf("✅ email ditemukan: %s", email)
	}

	// Ambil userID dari database berdasarkan email di sesi
	userID, oke := session.Values["user_id"].(int)
	if !oke {
		log.Println("❌ User ID tidak ditemukan di sesi")
		http.Error(w, "User ID tidak ditemukan", http.StatusUnauthorized)
		return
	} else {
		log.Printf("✅ User ID ditemukan: %d", userID)
	}

	// ✅ PERUBAHAN: Baca request body untuk mendapatkan perangkat yang dipilih dan besar_listrik
	var req AnalyzeRequest
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Printf("❌ Gagal membaca request body: %v", err)
		http.Error(w, "Gagal memproses permintaan analisis: data tidak valid", http.StatusBadRequest)
		return
	}

	if len(req.Devices) == 0 {
		http.Error(w, "Tidak ada perangkat yang dipilih untuk dianalisis", http.StatusBadRequest)
		log.Println("❌ Tidak ada perangkat yang diterima untuk analisis.")
		return
	}

	// Gunakan devices dan besarListrik dari request body
	devices := req.Devices
	besarListrik := req.BesarListrik // Menggunakan besar_listrik yang dikirim dari frontend

	log.Printf("✅ Menganalisis %d perangkat yang dipilih dari frontend.", len(devices))

	// Hitung total konsumsi harian (Wh) dari perangkat yang dipilih
	totalDailyWattHours := 0
	for _, device := range devices {
		if device.Power < 0 || device.Duration < 0 {
			http.Error(w, "Data perangkat tidak valid: Power atau Duration negatif", http.StatusBadRequest)
			return
		}
		totalDailyWattHours += device.Power * device.Duration
	}

	// Konversi ke kWh
	dailyKWh := float64(totalDailyWattHours) / 1000.0
	monthlyKWh := dailyKWh * 30

	// Ambil tarif berdasarkan besar_listrik yang diterima
	tariffRate := getTariffRate(besarListrik)
	estimatedMonthlyCost := monthlyKWh * tariffRate

	log.Println("🔹 Konsumsi harian (Wh):", totalDailyWattHours)
	log.Println("🔹 Konsumsi harian (kWh):", dailyKWh)
	log.Println("🔹 Konsumsi bulanan (kWh):", monthlyKWh)
	log.Println("🔹 Besar listrik (dari FE):", besarListrik)
	log.Println("🔹 Tarif per kWh:", tariffRate)
	log.Println("🔹 Biaya per bulan:", estimatedMonthlyCost)

	// Bangun prompt untuk AI dari perangkat yang dipilih
	prompt := buildPrompt(devices)

	log.Println("🔹 Prompt AI:", prompt)

	// Kirim ke AI
	ctx := r.Context()
	sessionAI := model.StartChat()
	resp, err := sessionAI.SendMessage(ctx, genai.Text(prompt))
	if err != nil {
		log.Printf("❌ Error sending message to AI: %v", err)
		http.Error(w, "Gagal mendapatkan respons dari AI", http.StatusInternalServerError)
		return
	}

	// Format respons AI
	aiResponse := formatAIResponse(resp)
	var idSubmit string
	var riwayatID int
	err = db.DB.QueryRow(`
        SELECT id_submit, id
        FROM riwayat_perangkat
        WHERE user_id = ?
        ORDER BY tanggal_input DESC, id DESC
        LIMIT 1`, userID).Scan(&idSubmit, &riwayatID)

	if err != nil {
		if err == sql.ErrNoRows {
			log.Println("⚠️ Belum ada data perangkat yang disimpan di riwayat untuk user ini. Analisis AI akan disimpan tanpa riwayat_id yang eksplisit.")
			riwayatID = 0
		} else {
			log.Printf("❌ Gagal mendapatkan id_submit terakhir untuk penyimpanan analisis: %v", err)
		}
	}
	log.Printf("✅ Menggunakan id_submit terakhir (jika ada) untuk penyimpanan analisis: %s dan riwayatID: %d", idSubmit, riwayatID)

	// ✅ Simpan ke database (perhatikan riwayatID bisa 0 jika tidak ada riwayat)
	err = SaveAnalysisToDB(userID, riwayatID, totalDailyWattHours, dailyKWh, aiResponse, estimatedMonthlyCost)
	if err != nil {
		log.Printf("❌ Gagal menyimpan hasil analisis ke database: %v", err)
		// Opsional: tetap kirim response meskipun gagal simpan
	} else {
		log.Println("✅ Hasil analisis berhasil disimpan.")
	}

	// Kirim respons ke frontend
	response := map[string]interface{}{
		"total_power_wh":       totalDailyWattHours,
		"daily_kwh":            dailyKWh,
		"monthly_kwh":          monthlyKWh,
		"tariff_rate":          tariffRate,
		"estimated_monthly_rp": formatRupiah(estimatedMonthlyCost),
		"ai_response":          aiResponse,
		"id_submit":            idSubmit,
		"besar_listrik":        besarListrik,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Error encoding JSON response: %v", err)
		http.Error(w, "Gagal mengirim respons", http.StatusInternalServerError)
	} else {
		log.Println("✅ Respons berhasil dikirim")
	}
}

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

func SaveAnalysisToDB(userID int, riwayatID int, totalPowerWh int, totalPowerKWh float64, aiResponse string, estimatedCost float64) error {
	estimatedCostRp := formatRupiah(estimatedCost)

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
		return fmt.Errorf("gagal menyimpan hasil analisis: %v", err)
	}

	log.Printf("✅ Hasil analisis berhasil disimpan untuk user %d dan riwayat_id %d", userID, riwayatID)
	return nil
}

func getTariffRate(besarListrik string) float64 {
	switch {
	case strings.Contains(besarListrik, "900"):
		return 1352 // R-1/TR - 900 VA
	case strings.Contains(besarListrik, "1300"), strings.Contains(besarListrik, "2200"):
		return 1444.70 // R-1/TR - 1300/2200 VA
	case strings.Contains(besarListrik, "3500"), strings.Contains(besarListrik, "4400"), strings.Contains(besarListrik, "5500"):
		return 1699.53 // R-2/TR
	case strings.Contains(besarListrik, "6600"), strings.Contains(besarListrik, "7700"), strings.Contains(besarListrik, "9000"):
		return 1699.53 // R-3/TR
	default:
		return 1500 // Tarif default jika tidak cocok
	}
}

// buildPrompt membuat prompt untuk AI berdasarkan daftar perangkat
func buildPrompt(devices []models.Device) string {
	var sb strings.Builder

	sb.WriteString("### Electricity Usage Analysis\n\n")

	if len(devices) > 0 {
		sb.WriteString(fmt.Sprintf("- Payment Type: %s\n", devices[0].Jenis_Pembayaran))
		sb.WriteString(fmt.Sprintf("- Household Power Capacity: %s\n", devices[0].Besar_Listrik))
	}

	sb.WriteString("### Devices in Use:\n")
	for _, device := range devices {
		sb.WriteString(fmt.Sprintf("- %s (%d Watts), used for %d hours/day\n", device.Name, device.Power, device.Duration))
	}

	sb.WriteString("\n### Estimated Most Power-Hungry Device:\n")
	sb.WriteString("- Calculate monthly consumption of each device using the formula above.\n")
	sb.WriteString("- Identify the device with the highest total cost or kWh consumption.\n")
	sb.WriteString("- Compare usage among devices to determine which one consumes the most power in the household based on necessity.\n")

	sb.WriteString("Response format:\n")
	sb.WriteString("- Provide concise bullet points with only the results, no headers.\n")
	sb.WriteString("- Convert all values to kWh.\n")
	sb.WriteString("Also provide a brief and informative suggestion.\n")

	return sb.String()
}

// formatAIResponse membersihkan dan memformat respons dari AI agar lebih mudah dibaca
func formatAIResponse(resp *genai.GenerateContentResponse) string {
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return "Tidak ada respons yang diterima dari AI."
	}

	var aiResponse strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if text, ok := part.(genai.Text); ok {
			cleanText := strings.ReplaceAll(string(text), "*", "")
			aiResponse.WriteString(cleanText + "\n")
		}
	}

	result := aiResponse.String()
	result = strings.TrimSpace(result)                // Hilangkan spasi di awal dan akhir
	result = strings.ReplaceAll(result, "\n\n", "\n") // Hilangkan baris kosong berlebihan
	return result
}
