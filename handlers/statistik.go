package handlers

import (
	"EnerTrack-BE/db"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Struct untuk respons agar konsisten dengan types.ts di frontend
type ChartDataPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

type CategoryChartData struct {
	Name       string  `json:"name"`
	Percentage float64 `json:"percentage"`
	Color      string  `json:"color"`
	TotalPower float64 `json:"total_power"`
}

type DateRangeResponse struct {
	FirstDate *string `json:"firstDate"`
	LastDate  *string `json:"lastDate"`
}

// GetMonthlyStatisticsHandler mengambil data statistik bulanan
func GetMonthlyStatisticsHandler(w http.ResponseWriter, r *http.Request) {
	session, errSession := Store.Get(r, "elektronik_rumah_session")
	if errSession != nil {
		log.Printf("❌ GetMonthlyStatisticsHandler: Error getting session: %v", errSession)
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		log.Println("❌ GetMonthlyStatisticsHandler: Unauthorized, user_id not found in session")
		http.Error(w, `{"error": "Tidak terautentikasi"}`, http.StatusUnauthorized)
		return
	}

	rows, errQuery := db.DB.Query(`
        SELECT
            FLOOR((DAYOFMONTH(DATE(tanggal_input)) - 1) / 7) + 1 AS week_of_month,
            SUM(daya * durasi) AS total_power_wh
        FROM
            riwayat_perangkat
        WHERE
            user_id = ?
            AND MONTH(DATE(tanggal_input)) = MONTH(CURDATE())
            AND YEAR(DATE(tanggal_input)) = YEAR(CURDATE())
        GROUP BY
            week_of_month
        ORDER BY
            week_of_month;
    `, userID)

	if errQuery != nil {
		log.Printf("❌ GetMonthlyStatisticsHandler: Error executing query: %v", errQuery)
		http.Error(w, `{"error": "Gagal mengambil data statistik bulanan"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	powerByWeekNumber := make(map[int]float64)
	for rows.Next() {
		var weekOfMonth int
		var totalPowerWh float64
		if errScan := rows.Scan(&weekOfMonth, &totalPowerWh); errScan != nil {
			log.Printf("❌ GetMonthlyStatisticsHandler: Error scanning row: %v", errScan)
			http.Error(w, `{"error": "Gagal membaca data statistik bulanan"}`, http.StatusInternalServerError)
			return
		}
		powerByWeekNumber[weekOfMonth] = totalPowerWh / 1000.0 // Wh ke kWh
		log.Printf("✅ Month stats - Week %d: %.2f kWh", weekOfMonth, totalPowerWh/1000.0)
	}
	if errRows := rows.Err(); errRows != nil {
		log.Printf("❌ GetMonthlyStatisticsHandler: Error after iterating rows: %v", errRows)
	}

	var responseData []ChartDataPoint
	numWeeksToDisplay := 5
	for i := 1; i <= numWeeksToDisplay; i++ {
		responseData = append(responseData, ChartDataPoint{
			Label: fmt.Sprintf("W%d", i),
			Value: powerByWeekNumber[i],
		})
	}

	log.Printf("✅ Monthly statistics response: %+v", responseData)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseData)
}

// FUNGSI YANG DIPERBAIKI: GetWeeklyStatisticsHandler
func GetWeeklyStatisticsHandler(w http.ResponseWriter, r *http.Request) {
	session, errSession := Store.Get(r, "elektronik_rumah_session")
	if errSession != nil {
		log.Printf("❌ GetWeeklyStatisticsHandler: Error getting session: %v", errSession)
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		log.Println("❌ GetWeeklyStatisticsHandler: Unauthorized, user_id not found in session")
		http.Error(w, `{"error": "Tidak terautentikasi"}`, http.StatusUnauthorized)
		return
	}

	// Ambil parameter 'date' dan bersihkan dari cache buster
	dateQueryParam := r.URL.Query().Get("date")
	if queryIndex := strings.Index(dateQueryParam, "?"); queryIndex != -1 {
		dateQueryParam = dateQueryParam[:queryIndex]
	}

	var targetDateForWeek time.Time
	var errParse error

	if dateQueryParam == "" {
		targetDateForWeek = time.Now().Local()
		log.Printf("✅ GetWeeklyStatisticsHandler: No date param, using current date: %s", targetDateForWeek.Format("2006-01-02"))
	} else {
		targetDateForWeek, errParse = time.Parse("2006-01-02", dateQueryParam)
		if errParse != nil {
			log.Printf("❌ GetWeeklyStatisticsHandler: Invalid date format: '%s', error: %v", dateQueryParam, errParse)
			http.Error(w, `{"error": "Format tanggal tidak valid, gunakan YYYY-MM-DD"}`, http.StatusBadRequest)
			return
		}
		log.Printf("✅ GetWeeklyStatisticsHandler: Using date from param: %s", dateQueryParam)
	}

	weekday := targetDateForWeek.Weekday()
	daysToMonday := 0
	if weekday == time.Sunday {
		daysToMonday = -6
	} else {
		daysToMonday = -int(weekday - time.Monday)
	}

	startOfWeek := targetDateForWeek.AddDate(0, 0, daysToMonday)
	startOfWeek = time.Date(startOfWeek.Year(), startOfWeek.Month(), startOfWeek.Day(), 0, 0, 0, 0, startOfWeek.Location())
	endOfWeek := startOfWeek.AddDate(0, 0, 6)
	endOfWeek = time.Date(endOfWeek.Year(), endOfWeek.Month(), endOfWeek.Day(), 23, 59, 59, 999999999, endOfWeek.Location())

	log.Printf("✅ GetWeeklyStatisticsHandler: UserID: %d, Target: %s, StartOfWeek: %s, EndOfWeek: %s",
		userID, targetDateForWeek.Format("2006-01-02"), startOfWeek.Format("2006-01-02"), endOfWeek.Format("2006-01-02"))

	query := `
		SELECT
			DATE(tanggal_input) AS day_date,
			SUM(daya * durasi) AS total_power_wh
		FROM
			riwayat_perangkat
		WHERE
			user_id = ?
			AND DATE(tanggal_input) >= ?
			AND DATE(tanggal_input) <= ?
		GROUP BY
			DATE(tanggal_input)
		ORDER BY
			day_date;`

	rows, errQuery := db.DB.Query(query, userID, startOfWeek.Format("2006-01-02"), endOfWeek.Format("2006-01-02"))
	if errQuery != nil {
		log.Printf("❌ GetWeeklyStatisticsHandler: Error executing query: %v", errQuery)
		http.Error(w, `{"error": "Gagal mengambil data statistik mingguan"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	powerByDate := make(map[string]float64)
	for rows.Next() {
		var dayDateStr string
		var totalPowerWh float64
		if errScan := rows.Scan(&dayDateStr, &totalPowerWh); errScan != nil {
			log.Printf("❌ GetWeeklyStatisticsHandler: Error scanning row: %v", errScan)
			http.Error(w, `{"error": "Gagal membaca data statistik mingguan"}`, http.StatusInternalServerError)
			return
		}

		// === PERBAIKAN UTAMA DI SINI ===
		// Database mengembalikan string format RFC3339 (contoh: 2025-12-02T00:00:00Z)
		// Kita perlu potong biar jadi YYYY-MM-DD saja agar cocok dengan kuncinya
		
		cleanDateKey := dayDateStr
		// Jika ada huruf 'T', ambil bagian depannya saja
		if idx := strings.Index(dayDateStr, "T"); idx != -1 {
			cleanDateKey = dayDateStr[:idx]
		} else if len(dayDateStr) >= 10 {
			// Fallback: ambil 10 karakter pertama
			cleanDateKey = dayDateStr[:10]
		}
		
		powerByDate[cleanDateKey] = totalPowerWh / 1000.0 // Wh ke kWh
		log.Printf("✅ Weekly data found - RAW: %s, CLEAN KEY: %s, Power: %.2f kWh", dayDateStr, cleanDateKey, totalPowerWh/1000.0)
	}
	if errRows := rows.Err(); errRows != nil {
		log.Printf("❌ GetWeeklyStatisticsHandler: Error after iterating rows: %v", errRows)
	}

	log.Printf("✅ Power by date map: %+v", powerByDate)

	var responseData []ChartDataPoint
	daysOfWeekLabels := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	currentDayInLoop := startOfWeek

	for i := 0; i < 7; i++ {
		dateKey := currentDayInLoop.Format("2006-01-02")
		value := powerByDate[dateKey]
		responseData = append(responseData, ChartDataPoint{
			Label: daysOfWeekLabels[i],
			Value: value,
		})
		// Log lebih detail buat debugging
		log.Printf("🔍 Looking for key [%s] -> Found: %.2f kWh", dateKey, value)
		
		currentDayInLoop = currentDayInLoop.AddDate(0, 0, 1)
	}

	log.Printf("✅ Weekly statistics response: %+v", responseData)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseData)
}

// GetCategoryStatisticsHandler
func GetCategoryStatisticsHandler(w http.ResponseWriter, r *http.Request) {
	session, errSession := Store.Get(r, "elektronik_rumah_session")
	if errSession != nil {
		log.Printf("❌ GetCategoryStatisticsHandler: Error getting session: %v", errSession)
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		log.Println("❌ GetCategoryStatisticsHandler: Unauthorized, user_id not found in session")
		http.Error(w, `{"error": "Tidak terautentikasi"}`, http.StatusUnauthorized)
		return
	}

	rows, errQuery := db.DB.Query(`
        SELECT
            k.nama_kategori,
            COALESCE(SUM(rp.daya * rp.durasi), 0) AS total_power_wh
        FROM
            kategori k
        LEFT JOIN
            riwayat_perangkat rp ON rp.kategori_id = k.kategori_id 
            AND rp.user_id = ?
        GROUP BY
            k.kategori_id, k.nama_kategori
        HAVING
            SUM(rp.daya * rp.durasi) > 0
        ORDER BY
            total_power_wh DESC;
    `, userID)

	if errQuery != nil {
		log.Printf("❌ GetCategoryStatisticsHandler: Error executing query: %v", errQuery)
		http.Error(w, `{"error": "Gagal mengambil data statistik kategori"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var categoryStatsTemp []CategoryChartData
	var totalOverallPowerKWh float64

	for rows.Next() {
		var categoryName string
		var totalPowerWh float64
		if errScan := rows.Scan(&categoryName, &totalPowerWh); errScan != nil {
			log.Printf("❌ GetCategoryStatisticsHandler: Error scanning row: %v", errScan)
			http.Error(w, `{"error": "Gagal membaca data statistik kategori"}`, http.StatusInternalServerError)
			return
		}
		currentCategoryPowerKWh := totalPowerWh / 1000.0
		categoryStatsTemp = append(categoryStatsTemp, CategoryChartData{
			Name:       categoryName,
			TotalPower: currentCategoryPowerKWh,
		})
		totalOverallPowerKWh += currentCategoryPowerKWh
		log.Printf("✅ Category: %s, Power: %.2f kWh", categoryName, currentCategoryPowerKWh)
	}
	if errRows := rows.Err(); errRows != nil {
		log.Printf("❌ GetCategoryStatisticsHandler: Error after iterating rows: %v", errRows)
	}

	var finalCategoryStats []CategoryChartData
	defaultColors := []string{"#3B82F6", "#48C353", "#9333EA", "#FF8C33", "#EF4444", "#F59E0B", "#10B981", "#6366F1"}
	for i, stat := range categoryStatsTemp {
		percentage := 0.0
		if totalOverallPowerKWh > 0 {
			percentage = (stat.TotalPower / totalOverallPowerKWh) * 100
		}
		finalCategoryStats = append(finalCategoryStats, CategoryChartData{
			Name:       stat.Name,
			TotalPower: stat.TotalPower,
			Percentage: percentage,
			Color:      defaultColors[i%len(defaultColors)],
		})
	}

	log.Printf("✅ Category statistics response: %+v", finalCategoryStats)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(finalCategoryStats)
}

// GetDataRangeHandler
func GetDataRangeHandler(w http.ResponseWriter, r *http.Request) {
	session, errSession := Store.Get(r, "elektronik_rumah_session")
	if errSession != nil {
		log.Printf("❌ GetDataRangeHandler: Error getting session: %v", errSession)
		http.Error(w, `{"error": "Gagal mendapatkan sesi"}`, http.StatusInternalServerError)
		return
	}

	userID, ok := session.Values["user_id"].(int)
	if !ok {
		log.Println("❌ GetDataRangeHandler: Unauthorized, user_id not found in session")
		http.Error(w, `{"error": "Tidak terautentikasi"}`, http.StatusUnauthorized)
		return
	}

	var response DateRangeResponse
	query := `
		SELECT 
			MIN(DATE(tanggal_input)), 
			MAX(DATE(tanggal_input)) 
		FROM riwayat_perangkat 
		WHERE user_id = ?
	`
	errQuery := db.DB.QueryRow(query, userID).Scan(&response.FirstDate, &response.LastDate)
	if errQuery != nil {
		log.Printf("⚠️ GetDataRangeHandler: No data found for user ID %d, returning null dates. Error: %v", userID, errQuery)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(DateRangeResponse{FirstDate: nil, LastDate: nil})
		return
	}

	log.Printf("✅ Data range for user %d: %s to %s", userID, *response.FirstDate, *response.LastDate)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}