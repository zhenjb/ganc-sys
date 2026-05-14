package main

import (
	"log"
	"net/http"
	"os"

	"github.com/zhenjb/ganc-sys/internal/api"
	"github.com/zhenjb/ganc-sys/internal/handler"
	"github.com/zhenjb/ganc-sys/internal/repository"
	"github.com/zhenjb/ganc-sys/internal/service"
)

func main() {
	port := getenv("PORT", "8080")

	healthRepo := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepo)
	healthHandler := handler.NewHealthHandler(healthService)

	router := api.NewRouter(api.RouterDeps{
		HealthHandler: healthHandler,
	})

	addr := ":" + port
	log.Printf("ganc-sys backend API listening on http://localhost%s", addr)

	if err := http.ListenAndServe(addr, router.Routes()); err != nil {
		log.Fatal(err)
	}
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
