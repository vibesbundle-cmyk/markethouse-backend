package handlers

import (
	"net/http"
	"strconv"

	"markethouse/internal/models"
	"markethouse/internal/services"

	"github.com/gin-gonic/gin"
)

// ContactHandler exposes address-book syncing and People You May Know.
type ContactHandler struct {
	Service *services.ContactService
}

// maxSyncBatch caps how many address-book entries one request may upload.
const maxSyncBatch = 2000

// Sync imports the client's address book and reports which numbers already
// belong to a MarketHouse account. Body: { "contacts": [{"name","phone"}] }
func (h *ContactHandler) Sync(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req struct {
		Contacts []models.IncomingContact `json:"contacts"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Contacts) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contacts required"})
		return
	}
	if len(req.Contacts) > maxSyncBatch {
		req.Contacts = req.Contacts[:maxSyncBatch]
	}

	result, err := h.Service.SyncContacts(c.Request.Context(), userID, req.Contacts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to sync contacts"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// List returns the user's stored address book with active flags.
func (h *ContactHandler) List(c *gin.Context) {
	userID := c.GetInt64("user_id")

	contacts, active, err := h.Service.ListContacts(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load contacts"})
		return
	}
	if contacts == nil {
		contacts = []models.ContactView{}
	}

	c.JSON(http.StatusOK, gin.H{"contacts": contacts, "active_count": active})
}

// Clear wipes the user's stored address book without turning the toggle off.
func (h *ContactHandler) Clear(c *gin.Context) {
	userID := c.GetInt64("user_id")

	if err := h.Service.ClearContacts(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clear contacts"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "contacts cleared"})
}

// PeopleYouMayKnow returns suggested users from phone matches + mutuals.
func (h *ContactHandler) PeopleYouMayKnow(c *gin.Context) {
	userID := c.GetInt64("user_id")

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	users, err := h.Service.PeopleYouMayKnow(c.Request.Context(), userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build suggestions"})
		return
	}
	if users == nil {
		users = []models.MatchedUser{}
	}

	c.JSON(http.StatusOK, gin.H{"users": users})
}

// UpdateSettings toggles contact syncing. Turning it off wipes stored data.
// Body: { "contact_sync_enabled": true }
func (h *ContactHandler) UpdateSettings(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req struct {
		ContactSyncEnabled bool `json:"contact_sync_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	setting, err := h.Service.UpdateSetting(c.Request.Context(), userID, req.ContactSyncEnabled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update setting"})
		return
	}

	c.JSON(http.StatusOK, setting)
}

// GetSettings returns the user's current contact-sync preference.
func (h *ContactHandler) GetSettings(c *gin.Context) {
	userID := c.GetInt64("user_id")

	setting, err := h.Service.GetSetting(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load setting"})
		return
	}

	c.JSON(http.StatusOK, setting)
}
