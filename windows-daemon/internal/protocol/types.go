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
	EventNotificationAction   = "notification_action"
	EventNotificationDismiss  = "notification_dismiss"
	EventTouchpadEvent        = "touchpad_event"
	EventBiometricUnlock      = "biometric_unlock"
	EventRemoteAction         = "remote_action"
	EventOpenUrl              = "open_url"
	EventContactsRequest      = "contacts_request"
	EventContactsResponse     = "contacts_response"
	EventPhotosRequest        = "photos_request"
	EventPhotosResponse       = "photos_response"
	EventPhotoDownloadRequest = "photo_download_request"
	EventScreenMirrorRequest  = "screen_mirror_request"
	EventScreenMirrorFrame    = "screen_mirror_frame"
	EventScreenTouch          = "screen_touch"
	EventStorageMountRequest  = "storage_mount_request"
	EventStorageMountStatus   = "storage_mount_status"
	EventHotspotCommand       = "hotspot_command"
	EventHotspotStatus        = "hotspot_status"
	EventCallAudioBridge      = "call_audio_bridge"
	EventAppListRequest       = "app_list_request"
	EventAppListResponse      = "app_list_response"
	EventAppLaunchRequest     = "app_launch_request"
	EventScreenKey            = "screen_key"
	EventScreenText           = "screen_text"
	EventScreenDim            = "screen_dim"
	EventRingerCommand        = "ringer_command"
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

// NotificationActionItem represents an actionable button inside a notification.
type NotificationActionItem struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	IsReply bool   `json:"is_reply"`
}

// NotificationPayload contains Android notification data.
type NotificationPayload struct {
	ID          string                   `json:"id"`
	PackageName string                   `json:"package_name"`
	AppName     string                   `json:"app_name"`
	Title       string                   `json:"title"`
	Text        string                   `json:"text"`
	Timestamp   int64                    `json:"timestamp"`
	Key         string                   `json:"key,omitempty"`
	CanReply    bool                     `json:"can_reply,omitempty"`
	Actions     []NotificationActionItem `json:"actions,omitempty"`
}

// CallStatePayload represents the state of phone calls.
type CallStatePayload struct {
	State       string `json:"state"` // RINGING, OFFHOOK, IDLE
	PhoneNumber string `json:"phone_number"`
	CallerName  string `json:"caller_name"`
}

// CallActionPayload represents an answer/reject/dial action from PC to phone.
type CallActionPayload struct {
	Action string  `json:"action"` // ANSWER, REJECT, HANGUP, DIAL, SET_SPEAKER, SET_MUTE
	Number *string `json:"number,omitempty"`
	Value  *bool   `json:"value,omitempty"`
}

// MediaInfoPayload describes what music/media is currently playing.
type MediaInfoPayload struct {
	Source     string  `json:"source,omitempty"` // "windows", "mac", or "phone"
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Album      string  `json:"album"`
	IsPlaying  bool    `json:"is_playing"`
	PositionMs int64   `json:"position_ms,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Percent    float64 `json:"percent,omitempty"`
}

// MediaCommandPayload represents a playback control command sent from phone to PC.
type MediaCommandPayload struct {
	Action     string  `json:"action"` // PLAY, PAUSE, PLAY_PAUSE, NEXT, PREVIOUS, VOLUME_UP, VOLUME_DOWN, SEEK_PERCENT
	PositionMs int64   `json:"position_ms,omitempty"`
	Percent    float64 `json:"percent,omitempty"`
}

// PhoneCommandPayload represents a command sent from PC to control the phone.
type PhoneCommandPayload struct {
	Action     string  `json:"action"` // PLAY_PAUSE, PLAY, PAUSE, NEXT, PREVIOUS, VOLUME_UP, VOLUME_DOWN, MUTE, RING, STOP_RING, SEEK_PERCENT
	PositionMs int64   `json:"position_ms,omitempty"`
	Percent    float64 `json:"percent,omitempty"`
}

// ClipboardPayload represents synced clipboard text or image.
type ClipboardPayload struct {
	Text        string `json:"text"`
	Timestamp   int64  `json:"timestamp"`
	Type        string `json:"type,omitempty"`         // "text" or "image"
	ImageBase64 string `json:"image_base64,omitempty"` // Base64 encoded PNG
	MimeType    string `json:"mime_type,omitempty"`    // "image/png"
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

// SmsSendPayload represents a request to send an SMS from PC.
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

// FileAvailablePayload is sent to the phone when a file is ready to be downloaded from PC.
type FileAvailablePayload struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	FileSize    int64  `json:"file_size"`
	DownloadURL string `json:"download_url"`
	MimeType    string `json:"mime_type,omitempty"`
	Sender      string `json:"sender"`
	Timestamp   int64  `json:"timestamp"`
}

// FileUploadNotifyPayload is sent when a file is uploaded to PC.
type FileUploadNotifyPayload struct {
	ID        string `json:"id"`
	FileName  string `json:"file_name"`
	FileSize  int64  `json:"file_size"`
	Path      string `json:"path"`
	Sender    string `json:"sender"`
	Timestamp int64  `json:"timestamp"`
}

// NotificationReplyPayload is sent when a user replies to an app notification from PC.
type NotificationReplyPayload struct {
	NotificationKey string `json:"notification_key"`
	ActionIndex     int    `json:"action_index"`
	ReplyText       string `json:"reply_text"`
}

// NotificationActionPayload is sent when a user clicks an action button on a notification from PC.
type NotificationActionPayload struct {
	NotificationKey string `json:"notification_key"`
	ActionIndex     int    `json:"action_index"`
}

// NotificationDismissPayload is sent to dismiss/cancel a notification on phone or remove it on PC.
type NotificationDismissPayload struct {
	NotificationKey string `json:"notification_key"`
	NotificationID  string `json:"notification_id,omitempty"`
}

// TouchpadEventPayload is sent when the user uses the phone screen as a trackpad / remote.
type TouchpadEventPayload struct {
	Type    string  `json:"type"`              // "move", "click", "scroll", "key"
	DX      float32 `json:"dx,omitempty"`
	DY      float32 `json:"dy,omitempty"`
	Button  string  `json:"button,omitempty"`  // "left", "right", "middle"
	ScrollY int     `json:"scroll_y,omitempty"`
	Key     string  `json:"key,omitempty"`     // "LEFT", "RIGHT", "UP", "DOWN", "F5", "ESC", "ENTER", "SPACE", "VOL_UP", "VOL_DOWN", "MUTE"
}

// BiometricUnlockPayload carries biometric approval and optional PIN to unlock PC/Mac.
type BiometricUnlockPayload struct {
	Status    string `json:"status"` // "REQUESTED", "AUTHENTICATED", "REJECTED"
	UnlockPin string `json:"unlock_pin,omitempty"`
	Timestamp int64  `json:"timestamp"`
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

// ScreenMirrorRequestPayload requests starting or stopping screen mirroring.
type ScreenMirrorRequestPayload struct {
	Action  string `json:"action"` // "START", "STOP"
	Quality int    `json:"quality"`
}

// ScreenMirrorFramePayload carries a frame of the phone screen in base64 JPEG.
type ScreenMirrorFramePayload struct {
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Data      string `json:"data"` // base64 JPEG
	Timestamp int64  `json:"timestamp"`
}

// ScreenTouchPayload carries touch gestures to be injected into the phone screen.
type ScreenTouchPayload struct {
	Action string  `json:"action"` // "down", "move", "up"
	X      float32 `json:"x"`      // 0.0 to 1.0
	Y      float32 `json:"y"`      // 0.0 to 1.0
}

// StorageMountRequestPayload requests mounting or unmounting phone storage.
type StorageMountRequestPayload struct {
	Action string `json:"action"` // "START", "STOP", "STATUS"
}

// StorageMountStatusPayload carries WebDAV server details on the phone.
type StorageMountStatusPayload struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	URL     string `json:"url"`
	Path    string `json:"path"`
}

// HotspotCommandPayload carries commands to control phone mobile hotspot.
type HotspotCommandPayload struct {
	Action string `json:"action"` // "START", "STOP", "STATUS"
}

// HotspotStatusPayload carries the Wi-Fi credentials for instant connection.
type HotspotStatusPayload struct {
	Enabled  bool   `json:"enabled"`
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	IP       string `json:"ip,omitempty"`
}

// CallAudioBridgePayload carries real-time PCM audio for hands-free calls.
type CallAudioBridgePayload struct {
	Action     string `json:"action"`              // "START", "STOP", "DATA"
	Direction  string `json:"direction,omitempty"` // "PHONE_TO_PC", "PC_TO_PHONE"
	Data       string `json:"data,omitempty"`      // base64 PCM 16kHz
	SampleRate int    `json:"sample_rate,omitempty"`
}

// InstalledAppInfo holds information about a launchable app on the phone.
type InstalledAppInfo struct {
	Name        string `json:"name"`
	PackageName string `json:"package_name"`
}

// AppListResponsePayload contains the list of installed applications.
type AppListResponsePayload struct {
	Apps []InstalledAppInfo `json:"apps"`
}

// AppLaunchRequestPayload requests launching a specific app on the phone.
type AppLaunchRequestPayload struct {
	PackageName string `json:"package_name"`
}

// ScreenKeyPayload injects an Android keycode (Enter, Backspace, etc.).
type ScreenKeyPayload struct {
	KeyCode int `json:"key_code"`
}

// ScreenTextPayload injects typed text into the active Android input field.
type ScreenTextPayload struct {
	Text string `json:"text"`
}

// ScreenDimPayload controls pitch-black screen power saving during mirroring.
type ScreenDimPayload struct {
	Enabled bool `json:"enabled"`
}

// RingerCommandPayload sets the phone ringer mode ("NORMAL", "VIBRATE", "SILENT").
type RingerCommandPayload struct {
	Mode string `json:"mode"`
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
