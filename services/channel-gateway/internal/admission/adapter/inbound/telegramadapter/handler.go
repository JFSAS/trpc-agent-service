// Package telegramadapter authenticates Telegram webhooks before calling the
// durable admission boundary. It deliberately does not use the SDK webhook
// handler: an HTTP acknowledgement must follow the admission transaction.
package telegramadapter

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
)

const (
	maxBodyBytes = 1 << 20
	secretHeader = "X-Telegram-Bot-Api-Secret-Token"
)

// Acceptor returns success only after the immutable admission receipt commits.
// It is invoked for ignored events and interactions as well as text inputs.
type Acceptor interface {
	AcceptInbound(context.Context, domain.Inbound) (domain.Receipt, error)
}

type handler struct {
	accountID  string
	secretHash [sha256.Size]byte
	acceptor   Acceptor
}

// ReplyContext is the minimum provider-specific address retained for delivery.
// Callback data is deliberately excluded: interaction handling is not prompting.
type ReplyContext struct {
	ChatID          string `json:"chat_id,omitempty"`
	MessageThreadID string `json:"message_thread_id,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
	CallbackQueryID string `json:"callback_query_id,omitempty"`
	InlineMessageID string `json:"inline_message_id,omitempty"`
}

// NewHandler binds a trusted, stable account ID to one registered webhook route.
// The secret must use Telegram's 1-256 character [A-Za-z0-9_-] alphabet. Account
// IDs are opaque ASCII identifiers (1-128 letters/digits/dot/colon/dash/underscore).
// The caller owns route registration, HTTP deadlines, and secret rotation.
func NewHandler(accountID string, webhookSecret string, acceptor Acceptor) (http.Handler, error) {
	if !identifier(accountID, 128, true) || !identifier(webhookSecret, 256, false) || acceptor == nil {
		return nil, errors.New("invalid Telegram webhook configuration")
	}
	return &handler{accountID: accountID, secretHash: sha256.Sum256([]byte(webhookSecret)), acceptor: acceptor}, nil
}

func identifier(s string, limit int, allowDot bool) bool {
	if len(s) == 0 || len(s) > limit {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || allowDot && (c == '.' || c == ':') {
			continue
		}
		return false
	}
	return true
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		respond(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	values := r.Header.Values(secretHeader)
	provided := sha256.Sum256([]byte(r.Header.Get(secretHeader)))
	if len(values) != 1 || subtle.ConstantTimeCompare(provided[:], h.secretHash[:]) != 1 {
		respond(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.ContentLength > maxBodyBytes {
		respond(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			respond(w, http.StatusRequestEntityTooLarge, "request_too_large")
		} else {
			respond(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	inbound, err := normalize(data, h.accountID, time.Now().UTC())
	if err != nil {
		respond(w, http.StatusBadRequest, "invalid_request")
		return
	}
	_, err = h.acceptor.AcceptInbound(r.Context(), inbound)
	switch {
	case err == nil:
		respond(w, http.StatusOK, "")
	case errors.Is(err, domain.ErrInvalidInput):
		respond(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, domain.ErrConflict):
		respond(w, http.StatusConflict, "event_conflict")
	default:
		// Includes transaction/commit failures and unknown infrastructure errors.
		// No provider success is emitted when durable acceptance is uncertain.
		w.Header().Set("Retry-After", "1")
		respond(w, http.StatusServiceUnavailable, "temporarily_unavailable")
	}
}

func respond(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if code == "" {
		_, _ = io.WriteString(w, `{"ok":true}`+"\n")
		return
	}
	// code is a local constant, never a provider payload or a database error.
	_, _ = io.WriteString(w, `{"ok":false,"error":"`+code+`"}`+"\n")
}

func normalize(data []byte, accountID string, now time.Time) (domain.Inbound, error) {
	canonical, fields, err := canonicalObject(data)
	if err != nil {
		return domain.Inbound{}, err
	}
	id, present := fields["update_id"]
	if !present || id == nil {
		return domain.Inbound{}, domain.ErrInvalidInput
	}
	var update models.Update
	if err := json.Unmarshal(canonical, &update); err != nil || update.ID < 0 {
		return domain.Inbound{}, domain.ErrInvalidInput
	}
	// The Bot API Update union permits at most one optional event member.
	eventCount := 0
	for key, value := range fields {
		// encoding/json matches struct field names case-insensitively. Provider
		// protocol members are case-sensitive; reject aliases instead of letting
		// them bypass the union check or override canonical known members.
		lower := strings.ToLower(key)
		if key != lower && (lower == "update_id" || updateEventKinds[lower]) {
			return domain.Inbound{}, domain.ErrInvalidInput
		}
		if value != nil && updateEventKinds[key] {
			eventCount++
		}
	}
	if eventCount > 1 {
		return domain.Inbound{}, domain.ErrInvalidInput
	}
	digest := sha256.Sum256(canonical)
	in := domain.Inbound{
		Key:  domain.EventKey{Provider: "telegram", AccountID: accountID, EventID: strconv.FormatInt(update.ID, 10)},
		Kind: "ignore", SourceDigest: hex.EncodeToString(digest[:]), ReceivedAt: now,
	}
	var reply ReplyContext
	switch {
	case update.CallbackQuery != nil:
		callback := update.CallbackQuery
		if callback.ID == "" || callback.From.ID <= 0 {
			return domain.Inbound{}, domain.ErrInvalidInput
		}
		in.SenderID = strconv.FormatInt(callback.From.ID, 10)
		reply.CallbackQueryID = callback.ID
		reply.InlineMessageID = callback.InlineMessageID
		if message := callback.Message.Message; message != nil {
			setMessageAddress(&in, &reply, message.Chat.ID, message.ID, message.MessageThreadID)
		} else if message := callback.Message.InaccessibleMessage; message != nil {
			setMessageAddress(&in, &reply, message.Chat.ID, message.MessageID, 0)
		}
		rawCallback, _ := fields["callback_query"].(map[string]any)
		if !callback.From.IsBot && actualHuman(rawCallback["from"]) {
			in.Kind = "interaction"
		}
	case update.Message != nil:
		message := update.Message
		setMessageAddress(&in, &reply, message.Chat.ID, message.ID, message.MessageThreadID)
		if message.From != nil {
			in.SenderID = strconv.FormatInt(message.From.ID, 10)
		}
		// First slice: only actual human text in a private chat. Group command
		// and reply-to-bot triggers require verified bot identity and remain
		// ignored until that capability is implemented. No media caption,
		// service event, edit, anonymous sender or bot becomes model input.
		rawMessage, _ := fields["message"].(map[string]any)
		if message.Chat.Type == models.ChatTypePrivate && message.Chat.ID > 0 && message.ID > 0 &&
			message.From != nil && message.From.ID > 0 && !message.From.IsBot &&
			message.SenderChat == nil && message.SenderBusinessBot == nil &&
			message.ViaBot == nil && message.EditDate == 0 && strings.TrimSpace(message.Text) != "" &&
			onlyTextMessage(rawMessage) && actualHuman(rawMessage["from"]) {
			in.Kind = "text"
			in.Text = message.Text
		}
	}
	in.ReplyContext, err = json.Marshal(reply)
	return in, err
}

func setMessageAddress(in *domain.Inbound, reply *ReplyContext, chatID int64, messageID, threadID int) {
	if chatID != 0 {
		in.ConversationID = strconv.FormatInt(chatID, 10)
		reply.ChatID = in.ConversationID
	}
	if messageID > 0 {
		reply.SourceMessageID = strconv.Itoa(messageID)
	}
	if threadID > 0 {
		in.ThreadID = strconv.Itoa(threadID)
		reply.MessageThreadID = in.ThreadID
	}
}

// Require the actual provider fields, not zero values supplied by JSON decoding.
func actualHuman(value any) bool {
	user, ok := value.(map[string]any)
	if !ok {
		return false
	}
	isBot, ok := user["is_bot"].(bool)
	if !ok || isBot {
		return false
	}
	id, ok := user["id"].(json.Number)
	if !ok {
		return false
	}
	number, err := id.Int64()
	return err == nil && number > 0
}

// A curated text envelope avoids treating a service/media payload containing a
// forged text member as a prompt. Unknown new message capabilities are audited
// as ignored rather than implicitly opting into execution.
func onlyTextMessage(fields map[string]any) bool {
	for key := range fields {
		if !textMessageFields[key] {
			return false
		}
	}
	return true
}

var textMessageFields = map[string]bool{
	"message_id": true, "message_thread_id": true, "from": true, "sender_chat": true,
	"sender_business_bot": true, "date": true, "chat": true, "text": true, "entities": true,
	"reply_to_message": true, "external_reply": true, "quote": true, "forward_origin": true,
	"is_topic_message": true, "is_automatic_forward": true, "via_bot": true, "edit_date": true,
	"has_protected_content": true, "is_from_offline": true, "link_preview_options": true,
	"effect_id": true, "reply_markup": true, "sender_boost_count": true, "sender_tag": true,
}

var updateEventKinds = map[string]bool{
	"message": true, "edited_message": true, "channel_post": true, "edited_channel_post": true,
	"business_connection": true, "business_message": true, "edited_business_message": true,
	"deleted_business_messages": true, "message_reaction": true, "message_reaction_count": true,
	"inline_query": true, "chosen_inline_result": true, "callback_query": true, "shipping_query": true,
	"pre_checkout_query": true, "purchased_paid_media": true, "poll": true, "poll_answer": true,
	"managed_bot": true, "guest_message": true, "my_chat_member": true, "chat_member": true,
	"chat_join_request": true, "chat_boost": true, "removed_chat_boost": true, "subscription": true,
	"stopped_message_generation": true,
}

// canonicalObject preserves JSON numbers losslessly, sorts object members on
// marshal, and rejects duplicate keys at every depth. This makes retry digests
// independent of whitespace/member order without silently accepting ambiguous
// event identities. Raw provider payload is retained only during the request.
func canonicalObject(data []byte) ([]byte, map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, nil, domain.ErrInvalidInput
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, 0)
	if err != nil {
		return nil, nil, domain.ErrInvalidInput
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, nil, domain.ErrInvalidInput
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, nil, domain.ErrInvalidInput
	}
	canonical, err := json.Marshal(fields)
	return canonical, fields, err
}

func readJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, domain.ErrInvalidInput
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := token.(string)
			if !ok {
				return nil, domain.ErrInvalidInput
			}
			if _, exists := object[key]; exists {
				return nil, domain.ErrInvalidInput
			}
			object[key], err = readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
		}
		_, err := decoder.Token()
		return object, err
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := decoder.Token()
		return array, err
	default:
		return nil, domain.ErrInvalidInput
	}
}
