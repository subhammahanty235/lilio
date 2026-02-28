package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

const (
	KeySize          = 32
	NonceSize        = 12
	SaltSize         = 16
	PBKDF2Iterations = 100000
)

// Encryptor handles encryption and decryption operations
type Encryptor struct {
	key []byte
}

// NewEncryptor creates a new encryptor with the given key
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(key))
	}
	return &Encryptor{key: key}, nil
}

// NewEncryptorFromPassword creates an encryptor from a password string
func NewEncryptorFromPassword(password string, salt []byte) (*Encryptor, error) {
	if len(salt) != SaltSize {
		return nil, fmt.Errorf("salt must be %d bytes", SaltSize)
	}
	key := pbkdf2.Key([]byte(password), salt, PBKDF2Iterations, KeySize, sha256.New)
	return NewEncryptor(key)
}

// GenerateSalt creates a random salt for key derivation
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}
	return salt, nil
}

// GenerateKey creates a random 32-byte encryption key
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM
func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM
func (e *Encryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < NonceSize+1 {
		return nil, errors.New("ciphertext too short")
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := ciphertext[:NonceSize]
	ciphertext = ciphertext[NonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong key or tampered data): %w", err)
	}

	return plaintext, nil
}

// EncryptionOverhead returns extra bytes added during encryption
func EncryptionOverhead() int {
	return NonceSize + 16
}

// HashKey creates a 32-byte key from any string using SHA-256
func HashKey(input string) []byte {
	hash := sha256.Sum256([]byte(input))
	return hash[:]
}

// EncryptWithPassword encrypts with auto-generated salt
func EncryptWithPassword(plaintext []byte, password string) ([]byte, error) {
	salt, err := GenerateSalt()
	if err != nil {
		return nil, err
	}

	enc, err := NewEncryptorFromPassword(password, salt)
	if err != nil {
		return nil, err
	}

	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		return nil, err
	}

	result := make([]byte, SaltSize+len(ciphertext))
	copy(result[:SaltSize], salt)
	copy(result[SaltSize:], ciphertext)

	return result, nil
}

// DecryptWithPassword decrypts password-encrypted data
func DecryptWithPassword(ciphertext []byte, password string) ([]byte, error) {
	if len(ciphertext) < SaltSize+NonceSize+1 {
		return nil, errors.New("ciphertext too short")
	}

	salt := ciphertext[:SaltSize]
	ciphertext = ciphertext[SaltSize:]

	enc, err := NewEncryptorFromPassword(password, salt)
	if err != nil {
		return nil, err
	}

	return enc.Decrypt(ciphertext)
}
