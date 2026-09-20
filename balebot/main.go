// Command balebot runs a Bale Messenger bot that lets customers order fruit
// online: browse the catalog, pick fruits and quantities, submit their
// address and phone number, then see an invoice and pay a deposit. Prices,
// per-fruit minimum order weight and order history are persisted in SQLite
// and can be managed from a small admin web panel.
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"balebot/admin"
	"balebot/bale"
	"balebot/bot"
	"balebot/db"
)

func main() {
	token := os.Getenv("BALE_BOT_TOKEN")
	if token == "" {
		log.Fatal("BALE_BOT_TOKEN environment variable is required")
	}

	dbPath := getEnv("DB_PATH", "balebot.db")
	photosDir := getEnv("PHOTOS_DIR", "photos")
	store, err := db.Open(dbPath, photosDir)
	if err != nil {
		log.Fatalf("opening database %s: %v", dbPath, err)
	}
	defer store.Close()

	cfg := bot.Config{
		DepositAmount:           200000,
		CardNumber:              getEnv("DEPOSIT_CARD_NUMBER", "xxxx-xxxx-xxxx-xxxx"),
		CardHolder:              getEnv("DEPOSIT_CARD_HOLDER", "نام صاحب حساب"),
		PaymentProviderToken:    os.Getenv("PAYMENT_PROVIDER_TOKEN"),
		PaymentCurrency:         getEnv("PAYMENT_CURRENCY", "IRR"),
		PaymentAmountMultiplier: 10,
	}
	if v := os.Getenv("DEPOSIT_AMOUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DepositAmount = n
		} else {
			log.Printf("ignoring invalid DEPOSIT_AMOUNT %q: %v", v, err)
		}
	}
	if v := os.Getenv("ADMIN_CHAT_ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.AdminChatID = n
		} else {
			log.Printf("ignoring invalid ADMIN_CHAT_ID %q: %v", v, err)
		}
	}
	if v := os.Getenv("PAYMENT_AMOUNT_MULTIPLIER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PaymentAmountMultiplier = n
		} else {
			log.Printf("ignoring invalid PAYMENT_AMOUNT_MULTIPLIER %q: %v", v, err)
		}
	}

	client := bale.NewClient(token)
	client.Debug = os.Getenv("DEBUG_UPDATES") == "1"
	if client.Debug {
		log.Println("DEBUG_UPDATES=1: raw Bale API responses will be logged (use only to verify the payment payload shape, then turn off)")
	}
	if cfg.PaymentProviderToken != "" {
		log.Printf("wallet payments enabled (currency=%s, amount multiplier=%d) — verify with one small real transaction before trusting it for customers", cfg.PaymentCurrency, cfg.PaymentAmountMultiplier)
	}
	b := bot.New(client, store, cfg)

	startAdminPanel(store, b)

	log.Println("fruit order bot is running...")
	if err := b.Run(); err != nil {
		log.Fatal(err)
	}
}

func startAdminPanel(store *db.Store, b *bot.Bot) {
	username := os.Getenv("ADMIN_USERNAME")
	password := os.Getenv("ADMIN_PASSWORD")
	if username == "" || password == "" {
		log.Println("ADMIN_USERNAME/ADMIN_PASSWORD not set, admin panel disabled")
		return
	}

	// Bound to localhost by default: reach it via `ssh -L 8080:127.0.0.1:8080`,
	// not by exposing it on the public interface.
	addr := getEnv("ADMIN_LISTEN_ADDR", "127.0.0.1:8080")
	server := admin.New(store, b, username, password)
	go func() {
		log.Printf("admin panel listening on %s", addr)
		if err := http.ListenAndServe(addr, server.Handler()); err != nil {
			log.Fatalf("admin panel failed: %v", err)
		}
	}()
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
