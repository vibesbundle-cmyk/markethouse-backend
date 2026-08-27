package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"markethouse/internal/handlers"
	"markethouse/internal/middleware"
	"markethouse/pkg/utils"

	"github.com/gin-gonic/gin"
)

// UploadsDir is set at startup so static serving works from any CWD
var UploadsDir = "uploads"

func SetupRouter(
	authHandler *handlers.AuthHandler,
	postHandler *handlers.PostHandler,
	followHandler *handlers.FollowHandler,
	interactionHandler *handlers.InteractionHandler,
	messageHandler *handlers.MessageHandler,
	wsHandler *handlers.WSHandler,
	marketHandler *handlers.MarketplaceHandler,
	shopHandler *handlers.ShopHandler,
	communityHandler *handlers.CommunityHandler,
	statusHandler *handlers.StatusHandler,
	notifHandler *handlers.NotificationHandler,
	commerceHandler *handlers.CommerceHandler,
	signalHandler *handlers.SignalHandler,
	supplyDemandHandler *handlers.SupplyDemandHandler,
	contactHandler *handlers.ContactHandler,
) *gin.Engine {

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// CORS must be registered before auth/rate-limit so preflight OPTIONS
	// requests from the Flutter web app are answered without a token.
	r.Use(middleware.CORS())

	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	})

	r.Use(globalRateLimiter())

	// ═══════════════════════════════════════════════════════════════
	// PUBLIC ROUTES
	// ═══════════════════════════════════════════════════════════════
	r.POST("/signup", authHandler.Signup)
	r.POST("/login", authHandler.Login)
	r.POST("/verify", authHandler.VerifyOTP)
	r.POST("/verify-phone", authHandler.VerifyPhone)
	r.POST("/resend-email", authHandler.ResendEmailOTP)
	r.POST("/resend-phone", authHandler.ResendPhoneOTP)
	r.POST("/refresh", authHandler.Refresh)
	r.POST("/forgot-password", authHandler.ForgotPassword)
	r.POST("/reset-password", authHandler.ResetPassword)
	r.GET("/username/check", authHandler.CheckUsername)

	// Public feed (public — no auth, doesn't know viewer)
	r.GET("/feed/business", postHandler.BusinessFeed)

	// Public marketplace
	r.GET("/supplies", marketHandler.PublicSupplies)
	r.GET("/demands", marketHandler.PublicDemands)

	// Public shop
	r.GET("/shop/products", shopHandler.PublicProducts)

	// ═══════════════════════════════════════════════════════════════
	// PROTECTED ROUTES
	// ═══════════════════════════════════════════════════════════════
	auth := r.Group("/")
	auth.Use(middleware.JWTAuthMiddleware())

	// ── USER ──────────────────────────────────────────────────────
	auth.POST("/upload/chat", authHandler.UploadMedia)
	auth.POST("/upload/media", authHandler.UploadMedia)
	auth.POST("/upload/profile", authHandler.UploadImage)
	auth.POST("/upload/header", authHandler.UploadImage)
	auth.PUT("/user/update", authHandler.UpdateProfile)
	auth.PUT("/user/location", authHandler.UpdateLocation)
	auth.GET("/user/hide-status-credit", authHandler.GetHideStatusCredit)
	auth.PUT("/user/hide-status-credit", authHandler.SetHideStatusCredit)
	auth.GET("/profile", authHandler.Profile)
	auth.GET("/user/:username", authHandler.GetPublicProfile)

	// ── FOLLOW ────────────────────────────────────────────────────
	auth.POST("/follow", followHandler.Follow)
	auth.POST("/unfollow", followHandler.Unfollow)
	auth.GET("/follow/stats/:user_id", followHandler.Stats)
	auth.GET("/follow/followers/:user_id", followHandler.GetFollowers)
	auth.GET("/follow/following/:user_id", followHandler.GetFollowing)

	// ── POSTS ─────────────────────────────────────────────────────
	auth.GET("/feed/public", postHandler.PublicFeed)
	auth.POST("/post", postHandler.CreatePost)
	auth.PUT("/post/:post_id", postHandler.EditPost)
	auth.DELETE("/post/:post_id", postHandler.DeletePost)
	auth.GET("/posts/:user_id", postHandler.UserPosts)
	auth.POST("/posts/:post_id/pin", postHandler.PinPost)
	auth.GET("/post/:post_id", postHandler.PostDetail)
	auth.GET("/posts/saved", interactionHandler.SavedPosts)
	auth.GET("/posts/liked", postHandler.LikedPosts)
	auth.GET("/posts/reshared", postHandler.ResharedPosts)
	auth.GET("/posts/reshared/:user_id", postHandler.UserResharedPosts)
	auth.GET("/feed/following", postHandler.FollowingFeed)
	auth.GET("/hashtags/trending", postHandler.TrendingHashtags)
	auth.GET("/hashtags/:tag/posts", postHandler.HashtagPosts)

	// ── RECOMMENDATION ENGINE ──────────────────────────────────────────────
	auth.POST("/signal", signalHandler.Record)
	auth.POST("/signal/commerce", signalHandler.RecordCommerce)
	auth.GET("/feed/for-you", signalHandler.ForYouFeed)
	auth.GET("/feed/trending", signalHandler.TrendingFeed)
	auth.GET("/feed/nearby", signalHandler.NearbyFeed)
	auth.GET("/nearby/users", signalHandler.NearbyUsers)
	auth.GET("/interests", signalHandler.GetInterests)
	auth.GET("/analytics/post/:post_id", signalHandler.PostAnalytics)
	auth.GET("/analytics/profile", signalHandler.CreatorAnalytics)
	auth.GET("/analytics/business", signalHandler.BusinessAnalytics)

	// ── INTERACTIONS ──────────────────────────────────────────────
	auth.POST("/like/:post_id", interactionHandler.Like)
	auth.DELETE("/like/:post_id", interactionHandler.Unlike)
	auth.POST("/comment/:post_id", interactionHandler.Comment)
	auth.GET("/comments/:post_id", interactionHandler.GetComments)
	auth.POST("/clikes/:comment_id", interactionHandler.LikeComment)
	auth.DELETE("/clikes/:comment_id", interactionHandler.UnlikeComment)
	auth.DELETE("/comments/:comment_id", interactionHandler.DeleteComment)
	auth.POST("/save/:post_id", interactionHandler.SavePost)
	auth.DELETE("/save/:post_id", interactionHandler.UnsavePost)
	auth.POST("/reshare/:post_id", interactionHandler.Reshare)
	auth.DELETE("/reshare/:post_id", interactionHandler.Unreshare)
	auth.GET("/reshares/:post_id", interactionHandler.GetReshares)

	// ── MARKETPLACE ───────────────────────────────────────────────
	auth.POST("/demand", marketHandler.CreateDemand)
	auth.POST("/supply", marketHandler.CreateSupply)
	auth.GET("/demands/mine", marketHandler.MyDemands)
	auth.GET("/supplies/mine", marketHandler.MySupplies)

	// ── SHOP ──────────────────────────────────────────────────────
	auth.POST("/shop/product", shopHandler.CreateProduct)
	auth.GET("/shop/products/mine", shopHandler.MyProducts)
	auth.POST("/shop/cart", shopHandler.AddToCart)
	auth.GET("/shop/cart", shopHandler.GetCart)
	auth.DELETE("/shop/cart/:item_id", shopHandler.RemoveFromCart)
	auth.POST("/shop/checkout", shopHandler.Checkout)
	auth.POST("/shop/checkout/confirm", shopHandler.ConfirmPayment)
	auth.POST("/shop/checkout/batch", shopHandler.CheckoutBatch)
	auth.POST("/shop/checkout/confirm-batch", shopHandler.ConfirmBatchPayment)
	auth.GET("/orders/mine", shopHandler.MyOrders)
	auth.POST("/orders/:id/deliver", shopHandler.ConfirmDelivery)
	auth.POST("/orders/:id/cancel/request", shopHandler.RequestCancel)
	auth.POST("/orders/:id/cancel/vendor", shopHandler.VendorApproveCancel)
	auth.POST("/orders/:id/cancel/admin", shopHandler.AdminApproveCancel)

	// ── WALLET ────────────────────────────────────────────────────
	auth.GET("/wallet", shopHandler.GetWallet)
	auth.GET("/wallet/history", shopHandler.WalletHistory)
	auth.POST("/wallet/deposit", shopHandler.Deposit)
	auth.POST("/wallet/withdraw", shopHandler.Withdraw)
	auth.POST("/wallet/send", shopHandler.Send)
	auth.GET("/wallet/pin", shopHandler.PinStatus)
	auth.POST("/wallet/pin", shopHandler.SetPin)
	auth.POST("/wallet/schedule", shopHandler.ScheduleTransfer)
	auth.DELETE("/wallet/schedule/:id", shopHandler.CancelSchedule)
	auth.GET("/wallet/schedule", shopHandler.ListScheduled)

	// ── GLOBAL SEARCH ──────────────────────────────────────────────
	auth.GET("/search", func(c *gin.Context) {
		q := c.Query("q")
		if q == "" {
			c.JSON(400, gin.H{"error": "q required"})
			return
		}
		db := communityHandler.DB

		var people []gin.H
		rows, _ := db.Query(`SELECT id,username,full_name,COALESCE(profile_photo,''),account_type FROM users
			WHERE username ILIKE $1 OR full_name ILIKE $1 LIMIT 10`, "%"+q+"%")
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				var uname, fn, photo, at string
				rows.Scan(&id, &uname, &fn, &photo, &at)
				people = append(people, gin.H{"id": id, "username": uname, "full_name": fn, "profile_photo": photo, "account_type": at})
			}
		}

		var communities []gin.H
		rows2, _ := db.Query(`SELECT id,name,slug,COALESCE(description,''),COALESCE(cover_photo,''),COALESCE(icon,''),member_count,COALESCE(category,'')
			FROM communities WHERE name ILIKE $1 OR description ILIKE $1 LIMIT 10`, "%"+q+"%")
		if rows2 != nil {
			defer rows2.Close()
			for rows2.Next() {
				var id, mc int64
				var name, slug, desc, cover, icon, cat string
				rows2.Scan(&id, &name, &slug, &desc, &cover, &icon, &mc, &cat)
				communities = append(communities, gin.H{"id": id, "name": name, "slug": slug, "description": desc, "cover_photo": cover, "icon": icon, "member_count": mc, "category": cat})
			}
		}

		var posts []gin.H
		rows3, _ := db.Query(`SELECT p.id,p.caption,COALESCE(p.media_url,''),p.created_at,u.username,COALESCE(u.profile_photo,'')
			FROM posts p JOIN users u ON u.id=p.user_id
			WHERE p.caption ILIKE $1 LIMIT 10`, "%"+q+"%")
		if rows3 != nil {
			defer rows3.Close()
			for rows3.Next() {
				var id int64
				var cap, media, ca, uname, photo string
				rows3.Scan(&id, &cap, &media, &ca, &uname, &photo)
				posts = append(posts, gin.H{"id": id, "caption": cap, "media_url": media, "created_at": ca, "username": uname, "profile_photo": photo})
			}
		}

		if people == nil {
			people = []gin.H{}
		}
		if communities == nil {
			communities = []gin.H{}
		}
		if posts == nil {
			posts = []gin.H{}
		}
		c.JSON(200, gin.H{"people": people, "communities": communities, "posts": posts})
	})

	// ── MESSAGING ─────────────────────────────────────────────────
	auth.POST("/message/send", messageHandler.SendMessage)
	auth.GET("/conversations", messageHandler.GetConversations)
	auth.GET("/conversation/:conv_id", messageHandler.GetConversation)
	auth.GET("/messages/:conv_id", messageHandler.GetHistory)
	auth.GET("/messages/:conv_id/pinned", messageHandler.GetPinnedMessages)
	auth.POST("/message/:msg_id/star", messageHandler.StarMessage)
	auth.GET("/messages/starred", messageHandler.GetStarredMessages)
	auth.POST("/message/:msg_id/pin", messageHandler.PinMessage)
	auth.POST("/message/:msg_id/react", messageHandler.ReactMessage)
	auth.PUT("/message/:msg_id", messageHandler.EditMessage)
	auth.DELETE("/message/:msg_id", messageHandler.DeleteMessage)
	auth.PUT("/conversation/:conv_id/settings", messageHandler.UpdateConversationSettings)
	auth.POST("/conversation/:conv_id/clear", messageHandler.ClearConversation)
 auth.POST("/conversation/:conv_id/hide", messageHandler.HideConversation)
 auth.POST("/conversation/:conv_id/purge", messageHandler.PurgeConversation)

	// ── REAL-TIME ─────────────────────────────────────────────────
	// WebSocket needs custom auth handling for query param tokens
	auth.GET("/ws", func(c *gin.Context) {
		// Check if token is in query params (common for WebSocket)
		token := c.Query("token")
		if token != "" {
			// Extract user_id from token and set it manually
			claims, err := utils.ValidateToken(token)
			if err == nil {
				c.Set("user_id", claims.UserID)
				c.Set("email", claims.Email)
			}
		}
		wsHandler.HandleWS(c)
	})

	// ── COMMUNITY ─────────────────────────────────────────────────
	auth.GET("/communities", communityHandler.List)
	auth.GET("/communities/:slug", communityHandler.Get)

	// ── COMMERCE (MARKETPLACE LISTINGS) ────────────────────────────
	auth.GET("/commerce", commerceHandler.List)
	auth.GET("/commerce/mine", commerceHandler.GetMine)
	auth.POST("/commerce/listing", commerceHandler.Create)
	auth.POST("/commerce/:id/vote", commerceHandler.Vote)
	auth.POST("/commerce/:id/report", commerceHandler.Report)
	auth.GET("/admin/commerce-reports", commerceHandler.ListReports)
	auth.PUT("/admin/commerce-reports/:id", commerceHandler.ResolveReport)

	// ---- Supply & Demand (thrift marketplace) ----
	auth.GET("/supply-demand", supplyDemandHandler.GetListings)
	auth.POST("/supply-demand", supplyDemandHandler.CreateListing)
	auth.PUT("/supply-demand/:id", supplyDemandHandler.UpdateListing)
	auth.DELETE("/supply-demand/:id", supplyDemandHandler.DeleteListing)
	auth.POST("/supply-demand/:id/interest", supplyDemandHandler.ExpressInterest)
	auth.GET("/supply-demand/mine", supplyDemandHandler.GetMyListings)
	auth.POST("/supply-demand/ask-around", supplyDemandHandler.PostAskAround)
	auth.GET("/supply-demand/nearby-suppliers", supplyDemandHandler.GetNearbySuppliers)
	auth.GET("/supplier-preferences", supplyDemandHandler.GetSupplierPreferences)
	auth.PUT("/supplier-preferences", supplyDemandHandler.SaveSupplierPreferences)
	// ---- Admin (fee config — checkout/escrow/wallet integration is pending, see handler notes) ----
	auth.GET("/admin/settings", supplyDemandHandler.GetSettings)
	auth.PUT("/admin/settings", supplyDemandHandler.UpdateSettings)
	auth.POST("/community", communityHandler.Create)
	// Routes with explicit path segments (most specific) come before wildcard routes
	auth.GET("/community/id/:id", communityHandler.GetByID)
	auth.POST("/community/:id/join", communityHandler.Join)
	auth.DELETE("/community/:id/leave", communityHandler.Leave)
	auth.DELETE("/community/:id", communityHandler.Delete)
	auth.GET("/community/:id/posts", communityHandler.GetPosts)
	auth.GET("/community/:id/members", communityHandler.GetMembers)
	auth.POST("/community/:id/role", communityHandler.AssignRole)
	auth.POST("/community/:id/post", communityHandler.CreatePost)
	auth.PUT("/community/:id/settings", communityHandler.UpdateSettings)
	auth.POST("/community/:id/ban", communityHandler.BanMember)
	auth.POST("/community/:id/mute", communityHandler.MuteMember)
	auth.GET("/community/:id/messages", communityHandler.GetMessages)
	auth.POST("/community/:id/messages", communityHandler.SendMessage)
	auth.PUT("/community/:id/messages/:mid", communityHandler.EditMessage)
	auth.DELETE("/community/:id/messages/:mid", communityHandler.DeleteMessage)
	auth.POST("/community/:id/messages/:mid/react", communityHandler.ReactMessage)
	auth.GET("/community/:id/messages/:mid/reactions/:emoji", communityHandler.ReactionUsers)
	auth.GET("/community/:id/online", communityHandler.Online)
	auth.POST("/community/:id/title", communityHandler.SetTitle)
	auth.POST("/community/:id/transfer", communityHandler.TransferOwnership)
	auth.GET("/me/referral", authHandler.GetReferral)
	auth.GET("/community/:id/can-call", communityHandler.CanCall)
	// Community marketplace
	auth.GET("/community/:id/listings", communityHandler.GetListings)
	auth.POST("/community/:id/listing", communityHandler.CreateListing)
	auth.DELETE("/community/listing/:id", communityHandler.DeleteListing)
	auth.POST("/community/listing/:id/sold", communityHandler.MarkListingSold)
	// Post-related routes (explicit "post" segment)
	auth.POST("/community/post/:post_id/vote", communityHandler.Vote)
	auth.POST("/community/post/:post_id/poll/vote", communityHandler.VotePoll)
	auth.POST("/community/post/:post_id/comment/:comment_id/best", communityHandler.MarkBestAnswer)
	auth.POST("/community/comment/:comment_id/like", communityHandler.LikeComment)
	auth.DELETE("/community/comment/:comment_id/like", communityHandler.UnlikeComment)
	auth.POST("/community/post/:post_id/pin", communityHandler.PinPost)
	auth.POST("/community/post/:post_id/lock", communityHandler.LockPost)
	auth.DELETE("/community/post/:post_id", communityHandler.DeletePost)
	auth.GET("/community/post/:post_id/comments", communityHandler.GetComments)
	auth.POST("/community/post/:post_id/comment", communityHandler.AddComment)

	// ── STATUS ────────────────────────────────────────────────────
	auth.GET("/statuses", statusHandler.GetFeed)
	auth.POST("/status", statusHandler.Create)
	auth.POST("/status/:id/view", statusHandler.View)
	auth.GET("/status/:id/views", statusHandler.Views)
	auth.POST("/status/:id/react", statusHandler.React)
	auth.DELETE("/status/:id", statusHandler.Delete)

	// ── NOTIFICATIONS ─────────────────────────────────────────────
	auth.GET("/notifications", notifHandler.GetAll)
	auth.GET("/notifications/unread-count", notifHandler.UnreadCount)
	auth.POST("/notifications/read", notifHandler.MarkRead)
	auth.GET("/notifications/prefs", notifHandler.GetPrefs)
	auth.PUT("/notifications/prefs", notifHandler.UpdatePrefs)
	auth.POST("/device/register", notifHandler.RegisterDevice)

	// ── CONTACT SYNCING ────────────────────────────────────────────
	auth.POST("/contacts/sync", contactHandler.Sync)
	auth.GET("/contacts", contactHandler.List)
	auth.DELETE("/contacts", contactHandler.Clear)
	auth.GET("/people-you-may-know", contactHandler.PeopleYouMayKnow)
	auth.GET("/settings/contacts", contactHandler.GetSettings)
	auth.PUT("/settings/contacts", contactHandler.UpdateSettings)

	// ── STATIC ────────────────────────────────────────────────────
	abs, _ := filepath.Abs(UploadsDir)
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		os.MkdirAll(abs, 0755)
	}
	r.Static("/uploads", abs)

	// ── HEALTH ────────────────────────────────────────────────────
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	return r
}

func globalRateLimiter() gin.HandlerFunc {
	type client struct {
		count   int
		resetAt time.Time
	}
	clients := make(map[string]*client)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()
		cl, ok := clients[ip]
		if !ok || now.After(cl.resetAt) {
			clients[ip] = &client{count: 1, resetAt: now.Add(time.Minute)}
			c.Next()
			return
		}
		cl.count++
		if cl.count > 120 {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		c.Next()
	}
}
