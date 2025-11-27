package handlers

import (
	"log"
	"net/http"

	"github.com/gorilla/sessions"
)

// ✅ Menggunakan kunci rahasia yang di-hardcode.
var Store = sessions.NewCookieStore(
	[]byte("kE7z$2n@p9sXv!cWbUjGf*aR5hL8yTqM"), // Ganti ini dengan string acak minimal 32 karakter
	[]byte("mY8s#pL!dF4gTj&b"),                 // Ganti ini dengan string acak yang PERSIS 16 KARAKTER
)

func init() {
	Store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,

		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	}
	log.Println("✅ Session Store initialized with hardcoded keys (for final project development).")
}
