package handlers

import (
	"html/template"
	"log"
	"net/http"
)

func HomeHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("home.html")
	if err != nil {
		http.Error(w, "Gagal memuat halaman utama", http.StatusInternalServerError)
		log.Println("❌ Error:", err)
		return
	}
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, "Gagal menampilkan halaman utama", http.StatusInternalServerError)
		log.Println("❌ Error:", err)
		return
	}
}
