package services

import (
	"markethouse/internal/models"
	"markethouse/internal/repository"
	"markethouse/internal/storage"
)

type MessageService struct {
	Repo    *repository.MessageRepo
	Hub     *Hub
	Storage storage.Storage
}

func (s *MessageService) SendMessage(senderID, receiverID int64, msg models.Message) (models.Message, error) {
	convID, err := s.Repo.GetOrCreateConversation(senderID, receiverID)
	if err != nil {
		return models.Message{}, err
	}
	msg.ConversationID = convID
	msg.SenderID = senderID
	msg.ReceiverID = receiverID
	if msg.MessageType == "" {
		msg.MessageType = "text"
	}

	if err = s.Repo.CreateMessage(&msg); err != nil {
		return models.Message{}, err
	}

	if s.Hub != nil {
		payload := map[string]interface{}{
			"type":            "new_message",
			"conversation_id": convID,
			"message":         msg,
		}
		s.Hub.SendToUser(receiverID, payload)
		s.Hub.SendToUser(senderID, payload)
	}
	return msg, nil
}

// GetChatHistory returns the conversation history. When the calling user is
// the receiver of pending messages, those are marked as read first — this is
// the "chat opened" moment that clears unread badges and read receipts.
func (s *MessageService) GetChatHistory(convID, userID int64) ([]models.Message, error) {
	if err := s.Repo.MarkMessagesRead(convID, userID); err != nil {
		return nil, err
	}
	// Let the other person know their messages were just read so their
	// open chat window can flip the ticks without a manual refresh.
	if s.Hub != nil {
		if conv, err := s.Repo.GetConversation(convID, userID); err == nil {
			s.Hub.SendToUser(conv.OtherUserID, map[string]interface{}{
				"type":            "messages_read",
				"conversation_id": convID,
				"reader_id":       userID,
			})
		}
	}
	return s.Repo.GetMessages(convID, userID, 200)
}

// ClearChat wipes the calling user's own view of a conversation. Messages
// stay intact for the other participant.
func (s *MessageService) ClearChat(convID, userID int64) error {
	return s.Repo.ClearConversation(convID, userID)
}

// HideChat hides a conversation from the caller's chat list.
func (s *MessageService) HideChat(convID, userID int64) error {
	return s.Repo.HideConversation(convID, userID)
}

// PurgeChat deletes the whole conversation for BOTH people — every message
// row and its media files. The chat disappears from both lists until one of
// them messages again, at which point it starts fresh.
func (s *MessageService) PurgeChat(convID, userID int64) error {
	media, err := s.Repo.PurgeConversation(convID, userID)
	for _, u := range media {
		if s.Storage != nil {
			s.Storage.Delete(u) // best effort
		}
	}
	if s.Hub != nil {
		if conv, cerr := s.Repo.GetConversation(convID, userID); cerr == nil {
			payload := map[string]interface{}{
				"type":            "conversation_purged",
				"conversation_id": convID,
			}
			s.Hub.SendToUser(conv.OtherUserID, payload)
			s.Hub.SendToUser(userID, payload)
		}
	}
	return err
}

func (s *MessageService) GetUserChats(userID int64) ([]repository.EnrichedConversation, error) {
	chats, err := s.Repo.GetConversations(userID)
	if err != nil {
		return nil, err
	}
	if s.Hub != nil {
		for i := range chats {
			chats[i].IsOnline = s.Hub.IsOnline(chats[i].OtherUserID)
		}
	}
	return chats, nil
}

func (s *MessageService) GetConversation(convID, userID int64) (repository.EnrichedConversation, error) {
	c, err := s.Repo.GetConversation(convID, userID)
	if err != nil {
		return c, err
	}
	if s.Hub != nil {
		c.IsOnline = s.Hub.IsOnline(c.OtherUserID)
	}
	return c, nil
}

func (s *MessageService) StarMessage(msgID int64, star bool) error {
	return s.Repo.StarMessage(msgID, star)
}
func (s *MessageService) PinMessage(msgID int64, pin bool) error {
	return s.Repo.PinMessage(msgID, pin)
}
func (s *MessageService) ReactMessage(msgID int64, reaction string) error {
	return s.Repo.ReactMessage(msgID, reaction)
}
func (s *MessageService) EditMessage(userID, msgID int64, content string) error {
	return s.Repo.EditMessage(userID, msgID, content)
}
func (s *MessageService) DeleteMessage(userID, msgID int64) error {
	// Look up the conversation so we can notify the other participant.
	conv, err := s.Repo.GetMessageConversation(msgID)
	if err != nil {
		// Best-effort: still try the delete even if we can't find the conv.
		return s.Repo.DeleteMessage(userID, msgID)
	}
	if err := s.Repo.DeleteMessage(userID, msgID); err != nil {
		return err
	}
	// Notify the other user in real-time so their chat shows the
	// deleted placeholder immediately instead of requiring a refresh.
	if s.Hub != nil {
		otherID := conv.OtherUserID
		if otherID != userID {
			s.Hub.SendToUser(otherID, map[string]interface{}{
				"type":            "message_deleted",
				"conversation_id": conv.ID,
				"message_id":      msgID,
				"sender_id":       userID,
			})
		}
	}
	return nil
}
func (s *MessageService) GetPinnedMessages(convID int64) ([]models.Message, error) {
	return s.Repo.GetPinnedMessages(convID)
}
func (s *MessageService) GetStarredMessages(userID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetStarredMessages(userID)
}
func (s *MessageService) SearchMessages(userID int64, query string) ([]map[string]interface{}, error) {
	return s.Repo.SearchMessages(userID, query)
}
func (s *MessageService) UpdateConversationSettings(convID int64, settings map[string]interface{}) error {
	return s.Repo.UpdateConversationSettings(convID, settings)
}
