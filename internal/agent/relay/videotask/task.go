// Package videotask binds opaque video task handles to their creator and upstream.
// Handles survive Agent restarts and load balancing without an in-memory task map.
package videotask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

const Prefix = "video_aigw1_"

// Task contains routing facts, never credentials. The signature also binds the
// upstream base URL, so editing a channel cannot send a task to a different host.
type Task struct {
	UserID       uint   `json:"u"`
	TokenID      uint   `json:"t"`
	Source       string `json:"s"`
	ChannelID    uint   `json:"c"`
	Model        string `json:"m"`
	RequestModel string `json:"r"`
	ID           string `json:"i"`
	Expires      int64  `json:"e"`
	encoded      string
	signature    []byte
}

func validSegment(id string, limit int) bool {
	return id != "" && id != "." && id != ".." && len(id) <= limit && !strings.ContainsAny(id, "/%?#\\\r\n")
}
func validID(id string) bool { return validSegment(id, 1024) }

func (t *Task) valid() bool {
	return t != nil && validID(t.ID) && t.TokenID != 0 && t.ChannelID != 0 && t.Model != "" && t.RequestModel != "" && (t.Source == "admin" || t.Source == "private")
}

func Parse(handle string) (*Task, error) {
	if !strings.HasPrefix(handle, Prefix) || len(handle) > 8192 {
		return nil, errors.New("invalid video task handle")
	}
	parts := strings.Split(strings.TrimPrefix(handle, Prefix), ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid video task handle")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var task Task
	if json.Unmarshal(raw, &task) != nil || !task.valid() || task.Expires <= time.Now().Unix() {
		return nil, errors.New("invalid or expired video task handle")
	}
	task.encoded = parts[0]
	task.signature, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(task.signature) != sha256.Size {
		return nil, errors.New("invalid video task signature")
	}
	return &task, nil
}

func signature(ch *models.Channel, encoded string) []byte {
	mac := hmac.New(sha256.New, []byte(ch.Key))
	mac.Write([]byte("aigw-video-task-v1\x00" + ch.GetBaseURL() + "\x00" + encoded))
	return mac.Sum(nil)
}

func (t *Task) Seal(ch *models.Channel) (string, error) {
	if ch == nil || ch.Key == "" || !t.valid() {
		return "", errors.New("video tasks require an upstream credential and a valid task ID")
	}
	t.Expires = time.Now().Add(7 * 24 * time.Hour).Unix()
	raw, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return Prefix + encoded + "." + base64.RawURLEncoding.EncodeToString(signature(ch, encoded)), nil
}

func (t *Task) Verify(ch *models.Channel, userID, tokenID uint) bool {
	return t != nil && ch != nil && ch.Key != "" && t.UserID == userID && t.TokenID == tokenID && t.Expires > time.Now().Unix() && hmac.Equal(t.signature, signature(ch, t.encoded))
}

// Path identifies only the registered video task operations.
func Path(path string) (handle, action string, ok bool) {
	const prefix = "/v1/videos/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 1 || len(parts) > 2 || !validSegment(parts[0], 8192) {
		return "", "", false
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "content", "remix":
			action = "/" + parts[1]
		default:
			return "", "", false
		}
	}
	return parts[0], action, true
}
