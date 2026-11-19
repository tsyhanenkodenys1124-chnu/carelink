package notify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/hkdf"
)

// TestEncryptIsReadableByBrowser повторює розшифрування на боці браузера
// за RFC 8291 і перевіряє, що повідомлення дійде у придатному вигляді.
func TestEncryptIsReadableByBrowser(t *testing.T) {
	clientKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ключ браузера: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("секрет підписки: %v", err)
	}
	subscription := Subscription{
		Endpoint: "https://push.example.org/abc",
		P256DH:   base64.RawURLEncoding.EncodeToString(clientKey.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(authSecret),
	}

	payload := []byte(`{"title":"Потрібна допомога","body":"Маріє Іванівно"}`)
	body, err := encrypt(payload, subscription)
	if err != nil {
		t.Fatalf("шифрування: %v", err)
	}

	decrypted, err := decryptAES128GCM(body, clientKey, authSecret)
	if err != nil {
		t.Fatalf("розшифрування: %v", err)
	}
	if string(decrypted) != string(payload) {
		t.Fatalf("після розшифрування текст змінився: %q", string(decrypted))
	}
}

// decryptAES128GCM виконує ті самі кроки, що й браузер під час прийому push.
func decryptAES128GCM(body []byte, clientKey *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, io.ErrUnexpectedEOF
	}
	salt := body[:16]
	recordSize := binary.BigEndian.Uint32(body[16:20])
	keyLength := int(body[20])
	serverPublicRaw := body[21 : 21+keyLength]
	ciphertext := body[21+keyLength:]
	if int(recordSize) != len(ciphertext)+16 {
		return nil, io.ErrUnexpectedEOF
	}

	serverPublic, err := ecdh.P256().NewPublicKey(serverPublicRaw)
	if err != nil {
		return nil, err
	}
	shared, err := clientKey.ECDH(serverPublic)
	if err != nil {
		return nil, err
	}
	keyInfo := append([]byte("WebPush: info\x00"), clientKey.PublicKey().Bytes()...)
	keyInfo = append(keyInfo, serverPublicRaw...)
	ikm := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, authSecret, keyInfo), ikm); err != nil {
		return nil, err
	}
	contentKey := make([]byte, 16)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), contentKey); err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(contentKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	// Останній байт — роздільник запису.
	return plaintext[:len(plaintext)-1], nil
}

func TestVAPIDTokenHasThreeSignedParts(t *testing.T) {
	public, private, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("ключі VAPID: %v", err)
	}
	sender, err := NewPushSender(public, private, "mailto:admin@doglyad.local")
	if err != nil {
		t.Fatalf("відправник push: %v", err)
	}
	token, err := sender.vapidToken("https://push.example.org/endpoint/xyz")
	if err != nil {
		t.Fatalf("токен VAPID: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("очікувалося три частини JWT, отримано %d", len(parts))
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("розбір заявок: %v", err)
	}
	var parsed struct {
		Audience string `json:"aud"`
		Subject  string `json:"sub"`
		Expires  int64  `json:"exp"`
	}
	if err := json.Unmarshal(claims, &parsed); err != nil {
		t.Fatalf("заявки JWT: %v", err)
	}
	if parsed.Audience != "https://push.example.org" {
		t.Fatalf("неправильна аудиторія: %q", parsed.Audience)
	}
	if parsed.Subject != "mailto:admin@doglyad.local" {
		t.Fatalf("неправильний суб'єкт: %q", parsed.Subject)
	}
	if parsed.Expires == 0 {
		t.Fatal("токен без терміну дії")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("розбір підпису: %v", err)
	}
	if len(signature) != 64 {
		t.Fatalf("підпис ES256 має містити 64 байти, отримано %d", len(signature))
	}
}

func TestNewPushSenderRejectsBrokenKeys(t *testing.T) {
	if _, err := NewPushSender("не-ключ", "теж-не-ключ", "mailto:a@b.c"); err == nil {
		t.Fatal("сервер прийняв неправильні ключі VAPID")
	}
}
