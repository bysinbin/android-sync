package protocol

import "encoding/json"

// Event type identifiers
const (
	EventDeviceInfo   = "device_info"
	EventNotification = "notification"
	EventCallState    = "call_state"
	EventMediaInfo    = "media_info"
	EventMediaCommand = "media_command"
	EventPhoneCommand = "phone_command"
	EventClipboard    = "clipboard"
	EventPing         = "ping"
	EventPong         = "pong"
)

// Message is the generic container for all sync events over WebSocket.
type Message struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// DeviceInfoPayload contains device metadata and battery status.
type DeviceInfoPayload struct {
	DeviceName   string `json:"device_name"`
	Model        string `json:"model"`
	BatteryLevel int    `json:"battery_level"`
	IsCharging   bool   `json:"is_charging"`
}

// NotificationPayload contains Android notification data.
type NotificationPayload struct {
	ID          string `json:"id"`
	PackageName string `json:"package_name"`
	AppName     string `json:"app_name"`
	Title       string `json:"title"`
	Text        string `json:"text"`
	Timestamp   int64  `json:"timestamp"`
}

// CallStatePayload represents the state of phone calls.
type CallStatePayload struct {
	State       string `json:"state"` // RINGING, OFFHOOK, IDLE
	PhoneNumber string `json:"phone_number"`
	CallerName  string `json:"caller_name"`
}

// MediaInfoPayload describes what music/media is currently playing.
type MediaInfoPayload struct {
	Source     string  `json:"source,omitempty"` // "mac" or "phone"
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Album      string  `json:"album"`
	IsPlaying  bool    `json:"is_playing"`
	PositionMs int64   `json:"position_ms,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Percent    float64 `json:"percent,omitempty"`
}

// MediaCommandPayload represents a playback control command.
type MediaCommandPayload struct {
	Action     string  `json:"action"` // PLAY, PAUSE, PLAY_PAUSE, NEXT, PREVIOUS, VOLUME_UP, VOLUME_DOWN, SEEK_PERCENT
	PositionMs int64   `json:"position_ms,omitempty"`
	Percent    float64 `json:"percent,omitempty"`
}

// PhoneCommandPayload represents a command sent from Mac to control the phone.
type PhoneCommandPayload struct {
	Action     string  `json:"action"` // PLAY_PAUSE, PLAY, PAUSE, NEXT, PREVIOUS, VOLUME_UP, VOLUME_DOWN, MUTE, RING, STOP_RING, SEEK_PERCENT
	PositionMs int64   `json:"position_ms,omitempty"`
	Percent    float64 `json:"percent,omitempty"`
}

// ClipboardPayload represents synced clipboard text.
type ClipboardPayload struct {
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

// Helper to construct a Message with serialized payload.
func NewMessage(event string, payload any) (*Message, error) {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Message{
		Event:   event,
		Payload: bytes,
	}, nil
}
