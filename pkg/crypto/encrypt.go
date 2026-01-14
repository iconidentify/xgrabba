// Package crypto provides encryption utilities for secure archive exports.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/argon2"
)

const (
	// File format magic bytes
	MagicBytes = "XGCR" // XGrabba CRypto

	// Version of the encryption format
	FormatVersion = 1

	// Argon2id parameters (OWASP recommended)
	Argon2Time    = 3
	Argon2Memory  = 64 * 1024 // 64 MB
	Argon2Threads = 4
	Argon2KeyLen  = 32 // AES-256

	// Salt and nonce sizes
	SaltSize  = 32
	NonceSize = 12 // GCM standard nonce size

	// Header size: magic(4) + version(4) + salt(32) + nonce(12) = 52 bytes
	HeaderSize = 4 + 4 + SaltSize + NonceSize
)

var (
	ErrInvalidMagic   = errors.New("invalid file format: not an encrypted xgrabba archive")
	ErrInvalidVersion = errors.New("unsupported encryption format version")
	ErrDecryptFailed  = errors.New("decryption failed: wrong password or corrupted data")
)

// DeriveKey derives an AES-256 key from a password using Argon2id.
func DeriveKey(password string, salt []byte) []byte {
	return argon2.IDKey(
		[]byte(password),
		salt,
		Argon2Time,
		Argon2Memory,
		Argon2Threads,
		Argon2KeyLen,
	)
}

// GenerateSalt creates a cryptographically secure random salt.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	return salt, nil
}

// Encrypt encrypts data using AES-256-GCM with the given password.
// Returns the encrypted data with header (magic + version + salt + nonce + ciphertext).
func Encrypt(plaintext []byte, password string) ([]byte, error) {
	// Generate random salt
	salt, err := GenerateSalt()
	if err != nil {
		return nil, err
	}

	// Derive key from password
	key := DeriveKey(password, salt)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Encrypt data
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Build output: magic + version + salt + nonce + ciphertext
	output := make([]byte, HeaderSize+len(ciphertext))
	copy(output[0:4], MagicBytes)
	binary.LittleEndian.PutUint32(output[4:8], FormatVersion)
	copy(output[8:8+SaltSize], salt)
	copy(output[8+SaltSize:HeaderSize], nonce)
	copy(output[HeaderSize:], ciphertext)

	return output, nil
}

// Decrypt decrypts data that was encrypted with Encrypt.
func Decrypt(data []byte, password string) ([]byte, error) {
	if len(data) < HeaderSize {
		return nil, ErrInvalidMagic
	}

	// Verify magic bytes
	if string(data[0:4]) != MagicBytes {
		return nil, ErrInvalidMagic
	}

	// Check version
	version := binary.LittleEndian.Uint32(data[4:8])
	if version != FormatVersion {
		return nil, ErrInvalidVersion
	}

	// Extract salt and nonce
	salt := data[8 : 8+SaltSize]
	nonce := data[8+SaltSize : HeaderSize]
	ciphertext := data[HeaderSize:]

	// Derive key from password
	key := DeriveKey(password, salt)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	// Decrypt data
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}

	return plaintext, nil
}

// EncryptFile encrypts a file and writes it to the destination.
func EncryptFile(srcPath, dstPath, password string) error {
	// Read source file
	plaintext, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}

	// Encrypt
	ciphertext, err := Encrypt(plaintext, password)
	if err != nil {
		return err
	}

	// Write to destination
	if err := os.WriteFile(dstPath, ciphertext, 0644); err != nil {
		return fmt.Errorf("write destination: %w", err)
	}

	return nil
}

// DecryptFile decrypts a file and returns its contents.
func DecryptFile(path, password string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return Decrypt(data, password)
}

// IsEncrypted checks if data appears to be an encrypted xgrabba archive.
func IsEncrypted(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return string(data[0:4]) == MagicBytes
}

// IsEncryptedFile checks if a file appears to be an encrypted xgrabba archive.
func IsEncryptedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	header := make([]byte, 4)
	if _, err := io.ReadFull(f, header); err != nil {
		return false
	}

	return string(header) == MagicBytes
}
