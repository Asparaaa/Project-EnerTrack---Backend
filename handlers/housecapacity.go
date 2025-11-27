package handlers

import (
	"encoding/json"
	"log"
	"net/http"
)

func GetHouseCapacityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Metode tidak diizinkan", http.StatusMethodNotAllowed)
		return
	}

	houseCapacities := []string{
		"450 VA",
		"900 VA",
		"1.300 VA",
		"2.200 VA",
		"3.500 VA",
		"4.400 VA",
		"5.500 VA",
		"6.600 VA ke atas",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(houseCapacities); err != nil {
		log.Printf("❌ Error encoding response: %v", err)
		http.Error(w, "Gagal mengirim response", http.StatusInternalServerError)
	}
}
