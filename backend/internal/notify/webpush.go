package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/crypto/hkdf"
)

// PushSender шифрує та надсилає Web Push за RFC 8291 з підписом VAPID (RFC 8292).
type PushSender struct {
	publicKey  string
	privateKey *ecdsa.PrivateKey
	rawPublic  []byte
	subject    string
	client     *http.Client
}

// NewPushSender розбирає ключі VAPID у форматі base64url.
//
// Приватний ключ — 32 байти скаляра, відкритий — 65 байтів нестисненої точки P-256.
func NewPushSender(publicKey, privateKey, subject string) (*PushSender, error) {
	rawPrivate, err := base64.RawURLEncoding.DecodeString(trimPadding(privateKey))
	if err != nil {
		return nil, fmt.Errorf("приватний ключ VAPID: %w", err)
	}
	if len(rawPrivate) != 32 {
		return nil, fmt.Errorf("приватний ключ VAPID має містити 32 байти, отримано %d", len(rawPrivate))
	}
	rawPublic, err := base64.RawURLEncoding.DecodeString(trimPadding(publicKey))
	if err != nil {
		return nil, fmt.Errorf("відкритий ключ VAPID: %w", err)
	}
	if len(rawPublic) != 65 || rawPublic[0] != 4 {
		return nil, errors.New("відкритий ключ VAPID має бути нестисненою точкою P-256 з 65 байтів")
	}
	signing := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(rawPublic[1:33]),
			Y:     new(big.Int).SetBytes(rawPublic[33:]),
		},
		D: new(big.Int).SetBytes(rawPrivate),
	}
	return &PushSender{
		publicKey:  base64.RawURLEncoding.EncodeToString(rawPublic),
		privateKey: signing,
		rawPublic:  rawPublic,
		subject:    subject,
		client:     &http.Client{Timeout: 20 * time.Second},
	}, nil
}

// Name повертає назву каналу.
func (p *PushSender) Name() string { return ChannelPush }

// Send шифрує повідомлення ключами підписки та надсилає його push-сервісу.
func (p *PushSender) Send(ctx context.Context, target Target, message Message) error {
	if target.Push.Endpoint == "" {
		return Permanent{Err: errors.New("підписка push не має адреси")}
	}
	payload, err := json.Marshal(map[string]any{
		"title":  message.Title,
		"body":   message.Body,
		"url":    message.URL,
		"kind":   message.Kind,
		"urgent": message.Urgent,
	})
	if err != nil {
		return Permanent{Err: fmt.Errorf("серіалізація push: %w", err)}
	}
	body, err := encrypt(payload, target.Push)
	if err != nil {
		return Permanent{Err: err}
	}
	token, err := p.vapidToken(target.Push.Endpoint)
	if err != nil {
		return Retryable{Err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.Push.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Permanent{Err: err}
	}
	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("TTL", strconv.Itoa(int((12 * time.Hour).Seconds())))
	request.Header.Set("Urgency", urgency(message.Urgent))
	request.Header.Set("Authorization", "vapid t="+token+", k="+p.publicKey)

	response, err := p.client.Do(request)
	if err != nil {
		return Retryable{Err: fmt.Errorf("запит до push-сервісу: %w", err)}
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))

	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return Permanent{Err: fmt.Errorf("підписку відкликано: %d", response.StatusCode)}
	case response.StatusCode == http.StatusRequestEntityTooLarge || response.StatusCode == http.StatusBadRequest:
		return Permanent{Err: fmt.Errorf("push-сервіс відхилив повідомлення: %d", response.StatusCode)}
	default:
		return Retryable{Err: fmt.Errorf("push-сервіс повернув %d", response.StatusCode)}
	}
}

func urgency(urgent bool) string {
	if urgent {
		return "high"
	}
	return "normal"
}

// vapidToken створює підписаний JWT ES256 для адреси push-сервісу.
func (p *PushSender) vapidToken(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("адреса push-сервісу: %w", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": parsed.Scheme + "://" + parsed.Host,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": p.subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, p.privateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("підпис VAPID: %w", err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// encrypt реалізує схему aes128gcm з RFC 8291.
func encrypt(payload []byte, sub Subscription) ([]byte, error) {
	clientPublicRaw, err := base64.RawURLEncoding.DecodeString(trimPadding(sub.P256DH))
	if err != nil {
		return nil, fmt.Errorf("ключ підписки p256dh: %w", err)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(trimPadding(sub.Auth))
	if err != nil {
		return nil, fmt.Errorf("секрет підписки auth: %w", err)
	}
	if len(authSecret) != 16 {
		return nil, fmt.Errorf("секрет підписки має містити 16 байтів, отримано %d", len(authSecret))
	}
	curve := ecdh.P256()
	clientPublic, err := curve.NewPublicKey(clientPublicRaw)
	if err != nil {
		return nil, fmt.Errorf("ключ підписки p256dh: %w", err)
	}
	serverPrivate, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("тимчасовий ключ: %w", err)
	}
	serverPublicRaw := serverPrivate.PublicKey().Bytes()

	shared, err := serverPrivate.ECDH(clientPublic)
	if err != nil {
		return nil, fmt.Errorf("узгодження ключів: %w", err)
	}

	// Крок 1: з спільного секрету та auth отримуємо вхідний матеріал ключа.
	keyInfo := append([]byte("WebPush: info\x00"), clientPublicRaw...)
	keyInfo = append(keyInfo, serverPublicRaw...)
	ikm := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, authSecret, keyInfo), ikm); err != nil {
		return nil, fmt.Errorf("похідний ключ підписки: %w", err)
	}

	// Крок 2: із випадкової солі формуємо ключ вмісту та одноразове число.
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("сіль шифрування: %w", err)
	}
	contentKey := make([]byte, 16)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00")), contentKey); err != nil {
		return nil, fmt.Errorf("ключ вмісту: %w", err)
	}
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00")), nonce); err != nil {
		return nil, fmt.Errorf("одноразове число: %w", err)
	}

	block, err := aes.NewCipher(contentKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// 0x02 позначає останній запис потоку.
	plaintext := append(append([]byte{}, payload...), 0x02)
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	recordSize := uint32(len(ciphertext) + 16)
	out := make([]byte, 0, 16+4+1+len(serverPublicRaw)+len(ciphertext))
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(serverPublicRaw)))
	out = append(out, serverPublicRaw...)
	out = append(out, ciphertext...)
	return out, nil
}

// GenerateVAPIDKeys створює нову пару ключів для Web Push.
func GenerateVAPIDKeys() (publicKey, privateKey string, err error) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(key.Bytes()), nil
}

// trimPadding прибирає символи вирівнювання, які додають деякі браузери.
func trimPadding(value string) string {
	for len(value) > 0 && value[len(value)-1] == '=' {
		value = value[:len(value)-1]
	}
	return value
}
