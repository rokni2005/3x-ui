// Command balebot runs a Bale Messenger bot that lets customers order fruit
// online: browse the catalog, pick fruits and quantities, submit their
// address and phone number, then see an invoice and pay a deposit.
package main

import (
	"log"
	"os"
	"strconv"

	"balebot/bale"
	"balebot/bot"
)

func main() {
	token := os.Getenv("BALE_BOT_TOKEN")
	if token == "" {
		log.Fatal("BALE_BOT_TOKEN environment variable is required")
	}

	cfg := bot.Config{
		DepositAmount: 200000,
		CardNumber:    getEnv("DEPOSIT_CARD_NUMBER", "xxxx-xxxx-xxxx-xxxx"),
		CardHolder:    getEnv("DEPOSIT_CARD_HOLDER", "نام صاحب حساب"),
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

	client := bale.NewClient(token)
	b := bot.New(client, cfg)

	log.Println("fruit order bot is running...")
	if err := b.Run(); err != nil {
		log.Fatal(err)
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
