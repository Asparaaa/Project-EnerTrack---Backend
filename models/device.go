// models/models.go atau di mana pun models.Device didefinisikan
package models

type Device struct {
	// ID dan UserID mungkin tidak perlu json tag jika tidak dikirim dari frontend
	// id_submit tidak perlu jika dibuat di backend
	Jenis_Pembayaran string  `json:"jenis_pembayaran"` // Sesuaikan dengan frontend
	Besar_Listrik    string  `json:"besar_listrik"`    // Sesuaikan dengan frontend
	Name             string  `json:"name"`             // Field frontend: 'name' (kecil)
	Brand            string  `json:"brand"`            // Field frontend: 'brand' (kecil)
	Power            int     `json:"power"`            // Field frontend: 'power' (kecil)
	Duration         int     `json:"duration"`         // Field frontend: 'duration' (kecil)
	CategoryID       int     `json:"category_id"`
	Weekly_Usage     float64 `json:"weekly_usage"`  // Optional, jika ingin dikirim/diterima
	Monthly_Usage    float64 `json:"monthly_usage"` // Optional
	Monthly_Cost     float64 `json:"monthly_cost"`  // Optional
	Tanggal_Input    string  `json:"tanggal_input"` // Optional
}
