package services

import (
	"markethouse/internal/models"
	"markethouse/internal/repository"
)

type MessageService struct {
	Repo *repository.MessageRepo
	Hub  *Hub
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

func (s *MessageService) GetChatHistory(convID int64) ([]models.Message, error) {
	return s.Repo.GetMessages(convID, 200)
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
	return s.Repo.DeleteMessage(userID, msgID)
}
func (s *MessageService) GetPinnedMessages(convID int64) ([]models.Message, error) {
	return s.Repo.GetPinnedMessages(convID)
}
func (s *MessageService) UpdateConversationSettings(convID int64, settings map[string]interface{}) error {
	return s.Repo.UpdateConversationSettings(convID, settings)
}
