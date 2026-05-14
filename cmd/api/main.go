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

	healthRepository := repository.NewHealthRepository()
	healthService := service.NewHealthService(healthRepository)
	healthHandler := handler.NewHealthHandler(healthService)

	mockRepository := repository.NewMockRepository()
	mockService := service.NewMockService(mockRepository)
	mockHandler := handler.NewMockHandler(mockService)

	router := api.NewRouter(api.RouterDeps{
		HealthHandler: healthHandler,
		MockHandler:   mockHandler,
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
