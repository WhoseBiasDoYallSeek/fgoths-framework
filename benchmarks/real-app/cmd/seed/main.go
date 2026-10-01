package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"real-app/internal/database"
)

func main() {
	count := 100
	if value := os.Getenv("BENCH_SEED_COUNT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			log.Fatalf("invalid BENCH_SEED_COUNT %q: %v", value, err)
		}
		count = parsed
	}

	db, err := database.Open()
	if err != nil {
		log.Fatalf("open benchmark database: %v", err)
	}
	defer db.Close()

	if err := database.SeedBenchmarkProducts(context.Background(), db, count); err != nil {
		log.Fatalf("seed benchmark products: %v", err)
	}
	log.Printf("seeded %d benchmark products", count)
}
