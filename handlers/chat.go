package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/iterator"
)

// Request body dari Android
// Kita tambah field 'context' untuk data perangkat spesifik
type ChatRequest struct {
	Message string `json:"message"`
	Context string `json:"context"` // Contoh isi: "AC Samsung, 400 Watt, Durasi 8 Jam"
}

// Handler untuk chat tanya jawab simpel dengan konteks perangkat
func ChatHandler(w http.ResponseWriter, r *http.Request, model *genai.GenerativeModel) {
	// 1. Validasi Method (Hanya POST)
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 2. Decode JSON dari Android
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	userMessage := req.Message
	if userMessage == "" {
		http.Error(w, "Pesan tidak boleh kosong", http.StatusBadRequest)
		return
	}

	log.Printf("📩 Chat masuk: %s | Context: %s", userMessage, req.Context)

	// 3. Bikin Prompt Khusus (System Prompt)
	// Ini rahasianya biar AI pinter: Kita selipin data perangkat di 'systemInstruction'
	var systemInstruction string
	
	if req.Context != "" {
		// Kalau user milih perangkat di dropdown
		systemInstruction = fmt.Sprintf(`
Peran: Kamu adalah asisten energi "EnerTrack" yang ahli.
Konteks Perangkat User: "%s"

Tugas: Jawab pertanyaan user SPESIFIK berdasarkan data perangkat di atas.
Batasan: 
- Jawab maksimal 2-3 kalimat. 
- Gunakan bahasa Indonesia yang santai, solutif, dan mudah dimengerti.
- Jika perangkat terkesan boros, berikan saran penghematan konkret.
`, req.Context)
	} else {
		// Kalau user milih "Umum"
		systemInstruction = `
Peran: Kamu adalah asisten energi "EnerTrack".
Tugas: Berikan tips hemat listrik secara umum.
Batasan: Jawab maksimal 2-3 kalimat. Bahasa Indonesia santai.
`
	}

	// Gabungkan instruksi + pertanyaan user
	finalPrompt := fmt.Sprintf("%s\nPertanyaan User: \"%s\"", systemInstruction, userMessage)

	// 4. Kirim ke Gemini
	ctx := r.Context()
	iter := model.GenerateContentStream(ctx, genai.Text(finalPrompt))

	// 5. Kumpulkan Response
	var fullResponse strings.Builder
	for {
		resp, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("❌ Error Gemini: %v", err)
			http.Error(w, "Gagal menghubungi AI", http.StatusInternalServerError)
			return
		}
		for _, cand := range resp.Candidates {
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					if text, ok := part.(genai.Text); ok {
						fullResponse.WriteString(string(text))
					}
				}
			}
		}
	}

	aiReply := fullResponse.String()
	// Bersihkan tanda bintang (*) biar gak berantakan di HP
	aiReply = strings.ReplaceAll(aiReply, "*", "") 

	// 6. Kirim Balik ke Android
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"reply": aiReply,
	})
}