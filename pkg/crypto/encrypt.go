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
	"sync"

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

// Encryptor provides fast bulk encryption with a pre-derived key.
// Use this for encrypting multiple files with the same password.
type Encryptor struct {
	key  []byte
	salt []byte
}

// NewEncryptor creates a new encryptor with a pre-derived key.
// This derives the key once using Argon2id, then all subsequent
// encryptions are fast (only AES-GCM operations).
func NewEncryptor(password string) (*Encryptor, error) {
	salt, err := GenerateSalt()
	if err != nil {
		return nil, err
	}

	key := DeriveKey(password, salt)
	return &Encryptor{key: key, salt: salt}, nil
}

// NewEncryptorWithSalt creates an encryptor with a specific salt.
// Used for decryption when the salt is known.
func NewEncryptorWithSalt(password string, salt []byte) *Encryptor {
	key := DeriveKey(password, salt)
	return &Encryptor{key: key, salt: salt}
}

// Salt returns the salt used for key derivation.
func (e *Encryptor) Salt() []byte {
	return e.salt
}

// Encrypt encrypts data using the pre-derived key.
// This is much faster than the standalone Encrypt function for bulk operations.
func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

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
	copy(output[8:8+SaltSize], e.salt)
	copy(output[8+SaltSize:HeaderSize], nonce)
	copy(output[HeaderSize:], ciphertext)

	return output, nil
}

// Decrypt decrypts data using the pre-derived key.
func (e *Encryptor) Decrypt(data []byte) ([]byte, error) {
	if len(data) < HeaderSize {
		return nil, ErrInvalidMagic
	}

	if string(data[0:4]) != MagicBytes {
		return nil, ErrInvalidMagic
	}

	version := binary.LittleEndian.Uint32(data[4:8])
	if version != FormatVersion {
		return nil, ErrInvalidVersion
	}

	nonce := data[8+SaltSize : HeaderSize]
	ciphertext := data[HeaderSize:]

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}

	return plaintext, nil
}

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
// Note: For bulk encryption of multiple files, use Encryptor instead for better performance.
func Encrypt(plaintext []byte, password string) ([]byte, error) {
	enc, err := NewEncryptor(password)
	if err != nil {
		return nil, err
	}
	return enc.Encrypt(plaintext)
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

// EncryptionJob represents a file to be encrypted
type EncryptionJob struct {
	SourcePath  string
	DestPath    string
	RelPath     string // Relative path for manifest
	EncName     string // Encrypted filename
}

// EncryptionResult represents the result of an encryption job
type EncryptionResult struct {
	Job   EncryptionJob
	Error error
}

// ParallelEncryptor encrypts multiple files in parallel using a shared key.
type ParallelEncryptor struct {
	encryptor   *Encryptor
	workers     int
	progressFn  func(completed, total int, currentFile string)
}

// NewParallelEncryptor creates a parallel encryptor with the specified number of workers.
func NewParallelEncryptor(password string, workers int, progressFn func(completed, total int, currentFile string)) (*ParallelEncryptor, error) {
	enc, err := NewEncryptor(password)
	if err != nil {
		return nil, err
	}
	if workers < 1 {
		workers = 4
	}
	return &ParallelEncryptor{
		encryptor:  enc,
		workers:    workers,
		progressFn: progressFn,
	}, nil
}

// Salt returns the salt used for key derivation.
func (p *ParallelEncryptor) Salt() []byte {
	return p.encryptor.Salt()
}

// Encryptor returns the underlying encryptor for single-file operations.
func (p *ParallelEncryptor) Encryptor() *Encryptor {
	return p.encryptor
}

// EncryptFiles encrypts multiple files in parallel.
// Returns a map of relative paths to encrypted filenames and any errors encountered.
func (p *ParallelEncryptor) EncryptFiles(jobs []EncryptionJob) (map[string]string, []error) {
	if len(jobs) == 0 {
		return map[string]string{}, nil
	}

	manifest := make(map[string]string)
	var manifestMu sync.Mutex
	var errors []error
	var errorsMu sync.Mutex

	jobChan := make(chan EncryptionJob, len(jobs))
	var wg sync.WaitGroup

	completed := 0
	var progressMu sync.Mutex

	// Start workers
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobChan {
				// Read file
				data, err := os.ReadFile(job.SourcePath)
				if err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Errorf("read %s: %w", job.SourcePath, err))
					errorsMu.Unlock()
					continue
				}

				// Encrypt
				encrypted, err := p.encryptor.Encrypt(data)
				if err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Errorf("encrypt %s: %w", job.SourcePath, err))
					errorsMu.Unlock()
					continue
				}

				// Write encrypted file
				if err := os.WriteFile(job.DestPath, encrypted, 0644); err != nil {
					errorsMu.Lock()
					errors = append(errors, fmt.Errorf("write %s: %w", job.DestPath, err))
					errorsMu.Unlock()
					continue
				}

				// Add to manifest
				manifestMu.Lock()
				manifest[job.RelPath] = job.EncName
				manifestMu.Unlock()

				// Update progress
				progressMu.Lock()
				completed++
				if p.progressFn != nil {
					p.progressFn(completed, len(jobs), job.RelPath)
				}
				progressMu.Unlock()
			}
		}()
	}

	// Send jobs
	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)

	// Wait for completion
	wg.Wait()

	return manifest, errors
}
