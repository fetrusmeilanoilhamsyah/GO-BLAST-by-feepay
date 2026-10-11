package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DATABASE_URL")
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	// Ensure there is at least one user
	var userID int64
	err = pool.QueryRow(context.Background(), "SELECT id FROM users WHERE role = 'admin' LIMIT 1").Scan(&userID)
	if err != nil {
		// Create dummy admin if none exists
		err = pool.QueryRow(context.Background(), "INSERT INTO users (email, password_hash, role, currency, referral_code, display_name) VALUES ('admin@feepay.com', 'dummy', 'admin', 'IDR', 'ADMIN1', 'Admin Feepay') RETURNING id").Scan(&userID)
		if err != nil {
			log.Fatal("Failed creating admin: ", err)
		}
	}

	// Insert campaign 1
	var campaignID int64
	err = pool.QueryRow(context.Background(), "INSERT INTO campaigns (name, status, button_mode, body, elements, created_by) VALUES ('Test Campaign A', 'draf', 'kartu_link', 'Ini pesan uji coba', '{}', $1) RETURNING id", userID).Scan(&campaignID)
	if err != nil {
		log.Fatal("Failed creating campaign: ", err)
	}

	fmt.Printf("Campaign dummy dibuat, ID: %d\n", campaignID)
}
