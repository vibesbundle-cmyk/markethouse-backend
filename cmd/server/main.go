package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"markethouse/internal/config"
	"markethouse/internal/database"
	"markethouse/internal/handlers"
	"markethouse/internal/repository"
	"markethouse/internal/routes"
	"markethouse/internal/services"
	"markethouse/internal/storage"
	"markethouse/pkg/utils"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// ---------------- ENVIRONMENT ----------------
	// Try multiple paths so it works regardless of working directory
	for _, p := range []string{".env", "../.env", "../../.env"} {
		if err := godotenv.Load(p); err == nil {
			// Found .env — change to its directory so upload paths are consistent
			dir := filepath.Dir(p)
			if dir != "." {
				abs, _ := filepath.Abs(dir)
				os.Chdir(abs)
			}
			break
		}
	}

	// ---------------- DATABASE ----------------
	db, err := database.ConnectPostgres()
	if err != nil {
		log.Fatalf("failed to connect to Postgres: %v", err)
	}

	redisClient, err := database.ConnectRedis()
	if err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}

	// ---------------- CONFIG ----------------
	cfg := config.LoadConfig()

	// JWT Secret — must be set via environment; no insecure fallback
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Fatal("JWT_SECRET environment variable is required")
	}
	utils.JWTSecret = []byte(jwtSecret)

	// ================== UPLOADS DIRECTORY ==================
	// FIXED: Ensure uploads directory and subdirectories exist
	// with proper permissions before any uploads are attempted
	uploadsDirs := []string{
		"uploads",
		"uploads/profile",
		"uploads/header",
		"uploads/posts",
		"uploads/supply",
		"uploads/chat",
		"uploads/status",
	}
	for _, dir := range uploadsDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("failed to create uploads directory %s: %v", dir, err)
		}
	}
	log.Println("✓ Upload directories initialized")

	// ---------------- STORAGE ----------------
	storageClient := &storage.LocalStorage{
		BaseURL: cfg.BaseURL,
	}

	// ---------------- AUTH ----------------
	authRepo := &repository.AuthRepo{DB: db}
	authService := &services.AuthService{
		Repo:    authRepo,
		Redis:   redisClient,
		Storage: storageClient,
	}
	authHandler := &handlers.AuthHandler{Service: authService}

	// ---------------- REAL-TIME HUB ----------------
	hub := services.NewHub(redisClient)
	go hub.Run()
	wsHandler := &handlers.WSHandler{Hub: hub}

	// ---------------- FOLLOW ----------------
	followRepo := &repository.FollowRepo{DB: db}
	followService := &services.FollowService{Repo: followRepo}
	followHandler := &handlers.FollowHandler{Service: followService}

	// ---------------- POST ----------------
	postRepo := &repository.PostRepo{DB: db}
	postService := &services.PostService{
		Repo:     postRepo,
		AuthRepo: authRepo,
		Storage:  storageClient,
	}
	postHandler := &handlers.PostHandler{Service: postService}

	// ---------------- INTERACTIONS ----------------
	interactionRepo := &repository.InteractionRepo{DB: db}
	interactionService := &services.InteractionService{Repo: interactionRepo}
	interactionHandler := &handlers.InteractionHandler{Service: interactionService, Hub: hub}

	// ---------------- MESSAGING ----------------
	messageRepo := &repository.MessageRepo{DB: db}
	messageService := &services.MessageService{
		Repo: messageRepo,
		Hub:  hub,
	}
	messageHandler := &handlers.MessageHandler{Service: messageService}

	// ---------------- MARKETPLACE ----------------
	marketRepo := repository.NewMarketplaceRepo(db)
	marketService := &services.MarketplaceService{
		Repo:    marketRepo,
		Storage: storageClient,
	}
	marketHandler := &handlers.MarketplaceHandler{Service: marketService}

	// ---------------- SHOP ----------------
	shopRepo := &repository.ShopRepo{DB: db}
	paymentProvider := config.NewPaymentProvider()
	shopService := &services.ShopService{
		Repo:    shopRepo,
		Storage: storageClient,
		Payment: paymentProvider,
	}
	shopHandler := &handlers.ShopHandler{Service: shopService}

	// ---------------- DELIVERY BREACH CRON ----------------
	// Every 15 minutes: mark paid orders whose delivery_date_scheduled has
	// passed as breached, refund the buyer, and restore stock.
	go func() {
		for range time.Tick(15 * time.Minute) {
			if err := shopService.ProcessOverdueOrders(); err != nil {
				log.Printf("overdue orders cron: %v", err)
			}
		}
	}()
	log.Println("✓ Delivery-breach cron scheduled (every 15m)")

	// ---------------- RECOMMENDATION ENGINE ----------------
	recService := &services.RecommendationService{DB: db, Redis: redisClient}
	signalHandler := &handlers.SignalHandler{DB: db, Redis: redisClient, Rec: recService}

	// ---------------- COMMUNITY ----------------
	communityHandler := &handlers.CommunityHandler{DB: db, Hub: hub}

	// ---------------- STATUS ----------------
	statusHandler := &handlers.StatusHandler{DB: db}

	// ---------------- NOTIFICATIONS ----------------
	notifHandler := &handlers.NotificationHandler{DB: db}

	// ---------------- COMMERCE ----------------
	commerceHandler := &handlers.CommerceHandler{DB: db, Storage: storageClient}

	// ---------------- SUPPLY & DEMAND (thrift marketplace + escrow wallet) ----------------
	supplyDemandHandler := &handlers.SupplyDemandHandler{DB: db, Hub: hub}

	// ---------------- CONTACT SYNCING ----------------
	contactRepo := &repository.ContactRepo{DB: db}
	contactService := &services.ContactService{Repo: contactRepo}
	contactHandler := &handlers.ContactHandler{Service: contactService}

	// ---------------- ROUTES ----------------
	r := routes.SetupRouter(authHandler, postHandler, followHandler, interactionHandler, messageHandler, wsHandler, marketHandler, shopHandler, communityHandler, statusHandler, notifHandler, commerceHandler, signalHandler, supplyDemandHandler, contactHandler)
	r.Use(func(c *gin.Context) { c.Set("db", db); c.Next() })

	// ---------------- START SERVER ----------------
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("failed to run server: %v", err)
	}
}
