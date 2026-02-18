package config

import (
	"log"

	"github.com/joho/godotenv"
)

// LoadEnv loads environment variables from a .env file.
// If the file doesn't exist, it silently continues (env vars may be set externally).
func LoadEnv(paths ...string) {
	if len(paths) == 0 {
		paths = []string{".env"}
	}
	if err := godotenv.Load(paths...); err != nil {
		log.Printf("[Config] No .env file found, using system environment variables")
	}
}
