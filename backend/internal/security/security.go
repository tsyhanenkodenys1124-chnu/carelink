// Пакет security містить генерацію ідентифікаторів, токенів і хешування паролів.
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrHashFormat повертається, коли збережений хеш пошкоджено.
var ErrHashFormat = errors.New("невідомий формат хешу пароля")

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// ID створює випадковий ідентифікатор запису.
func ID() string {
	b := make([]byte, 16)
	mustRead(b)
	return hex.EncodeToString(b)
}

// Token створює криптографічно стійкий токен для сесій, запрошень і пристроїв.
func Token() string {
	b := make([]byte, 32)
	mustRead(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// NumericCode створює код із указаної кількості цифр без передбачуваного джерела.
func NumericCode(digits int) string {
	if digits < 1 {
		digits = 6
	}
	var sb strings.Builder
	for i := 0; i < digits; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic("немає джерела випадковості: " + err.Error())
		}
		sb.WriteByte(byte('0' + n.Int64()))
	}
	return sb.String()
}

// Hash повертає SHA-256 у шістнадцятковому вигляді. Використовується для токенів у базі.
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// HashPassword створює хеш argon2id із випадковою сіллю.
func HashPassword(password string) string {
	salt := make([]byte, 16)
	mustRead(salt)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return "argon2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}

// CheckPassword порівнює пароль із хешем за сталий час.
func CheckPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" {
		return false
	}
	salt, errSalt := base64.RawStdEncoding.DecodeString(parts[1])
	want, errKey := base64.RawStdEncoding.DecodeString(parts[2])
	if errSalt != nil || errKey != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func mustRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("немає джерела випадковості: " + err.Error())
	}
}
