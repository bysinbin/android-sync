package protocol

import "encoding/json"

// Event type identifiers
const (
	EventDeviceInfo      = "device_info"
	EventNotification    = "notification"
	EventPCNotification  = "pc_notification"
	EventCallState       = "call_state"
	EventCallAction      = "call_action"
	EventMediaInfo       = "media_info"
	EventMediaCommand    = "media_command"
	EventPhoneCommand    = "phone_command"
	EventClipboard       = "clipboard"
	EventSmsSyncRequest  = "sms_sync_request"
	EventSmsSyncResponse = "sms_sync_response"
	EventSmsSend         = "sms_send"
	EventSmsSentStatus   = "sms_sent_status"
	EventSmsNewMessage   = "sms_new_message"
	EventAuthRequest     = "auth_request"
	EventAuthResponse    = "auth_response"
	EventPairConfirm          = "pair_confirm"
	EventUnpair               = "unpair"
	EventFileAvailable        = "file_available"
	EventFileUploadNotify     = "file_upload_notify"
	EventNotificationReply    = "notification_reply"
	EventRemoteAction         = "remote_action"
	EventOpenUrl              = "open_url"
	EventContactsRequest      = "contacts_request"
	EventContactsResponse     = "contacts_response"
	EventPhotosRequest        = "photos_request"
	EventPhotosResponse       = "photos_response"
	EventPhotoDownloadRequest = "photo_download_request"
	EventPing                 = "ping"
	EventPong                 = "pong"
)

// AuthRequestPayload is sent when a connection opens to initiate/check pairing.
type AuthRequestPayload struct {
	ClientID   string `json:"client_id"`
	ClientName string `json:"client_name"`
	OS         string `json:"os"`
	AuthToken  string `json:"auth_token,omitempty"`
	PairingPin string `json:"pairing_pin"`
}

// AuthResponsePayload is returned by the phone answering the auth request.
type AuthResponsePayload struct {
	Status     string `json:"status"` // "AUTHORIZED", "PAIRING_REQUIRED", "REJECTED"
	ClientName string `json:"client_name,omitempty"`
}

// PairConfirmPayload is sent when a user confirms or rejects pairing on the phone.
type PairConfirmPayload struct {
	Approved   bool   `json:"approved"`
	AuthToken  string `json:"auth_token,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
}

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
	Key         string `json:"key,omitempty"`
	CanReply    bool   `json:"can_reply,omitempty"`
}

// CallStatePayload represents the state of phone calls.
type CallStatePayload struct {
	State       string `json:"state"` // RINGING, OFFHOOK, IDLE
	PhoneNumber string `json:"phone_number"`
	CallerName  string `json:"caller_name"`
}

// CallActionPayload represents an answer/reject/dial action from PC/Mac to phone.
type CallActionPayload struct {
	Action string  `json:"action"` // ANSWER, REJECT, HANGUP, DIAL, SET_SPEAKER, SET_MUTE
	Number *string `json:"number,omitempty"`
	Value  *bool   `json:"value,omitempty"`
}

// MediaInfoPayload describes what music/media is currently playing.
type MediaInfoPayload struct {
	Source     string  `json:"source,omitempty"` // "mac", "windows", or "phone"
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

// SmsMessage represents an SMS message from/to phone.
type SmsMessage struct {
	ID          string `json:"id"`
	ThreadID    int64  `json:"thread_id"`
	Address     string `json:"address"`
	ContactName string `json:"contact_name,omitempty"`
	Body        string `json:"body"`
	Timestamp   int64  `json:"timestamp"`
	IsIncoming  bool   `json:"is_incoming"`
	Read        bool   `json:"read"`
}

// SmsSyncPayload represents the list of SMS messages.
type SmsSyncPayload struct {
	Messages []SmsMessage `json:"messages"`
}

// SmsSendPayload represents a request to send an SMS from Mac.
type SmsSendPayload struct {
	Recipient string `json:"recipient"`
	Body      string `json:"body"`
}

// SmsSentStatusPayload represents the delivery/sent status of an SMS.
type SmsSentStatusPayload struct {
	Success   bool   `json:"success"`
	Recipient string `json:"recipient"`
	Body      string `json:"body"`
	Error     string `json:"error,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// FileAvailablePayload is sent to the phone when a file is ready to be downloaded from Mac.
type FileAvailablePayload struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	DownloadURL string `json:"download_url"`
	MimeType    string `json:"mime_type,omitempty"`
	Sender      string `json:"sender"`
	Timestamp   int64  `json:"timestamp"`
}

// FileUploadNotifyPayload is sent when a file is uploaded to Mac.
type FileUploadNotifyPayload struct {
	ID        string `json:"id"`
	FileName  string `json:"file_name"`
	FileSize  int64  `json:"file_size"`
	Path      string `json:"path"`
	Sender    string `json:"sender"`
	Timestamp int64  `json:"timestamp"`
}

// NotificationReplyPayload is sent when a user replies to an app notification from Mac.
type NotificationReplyPayload struct {
	NotificationKey string `json:"notification_key"`
	ActionIndex     int    `json:"action_index"`
	ReplyText       string `json:"reply_text"`
}

// RemoteActionPayload carries remote control requests (lock, sleep, etc.)
type RemoteActionPayload struct {
	Action string `json:"action"` // "LOCK", "SLEEP", "SHUTDOWN", "RESTART"
	Param  string `json:"param,omitempty"`
}

// OpenUrlPayload carries a URL to be opened in default browser.
type OpenUrlPayload struct {
	URL    string `json:"url"`
	Sender string `json:"sender,omitempty"`
}

// ContactItem represents a contact entry from the phone.
type ContactItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
}

// ContactsResponsePayload contains the phone's contact list.
type ContactsResponsePayload struct {
	Contacts []ContactItem `json:"contacts"`
}

// PhotoItem represents a photo entry from the phone gallery.
type PhotoItem struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Date      int64  `json:"date"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mime_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

// PhotosResponsePayload contains the recent photos list from phone.
type PhotosResponsePayload struct {
	Photos []PhotoItem `json:"photos"`
	Count  int         `json:"count"`
}

// PhotoDownloadRequestPayload requests downloading full-res photo from phone.
type PhotoDownloadRequestPayload struct {
	ID        int64  `json:"id"`
	UploadURL string `json:"upload_url,omitempty"`
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

