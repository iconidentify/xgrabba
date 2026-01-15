// Package crypto provides encryption utilities for secure archive exports.
package crypto

import (
	"bufio"
	"context"
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
	FormatVersion   = 1 // Legacy: full-file encryption
	FormatVersionV2 = 2 // Streaming: chunked encryption

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

	// V2 streaming constants
	DefaultChunkSize = 1024 * 1024 // 1MB chunks for streaming
	GCMTagSize       = 16          // GCM authentication tag size

	// V2 Header size: magic(4) + version(4) + salt(32) + nonce(12) + chunkSize(4) = 56 bytes
	HeaderSizeV2 = 4 + 4 + SaltSize + NonceSize + 4

	// Pipeline constants for high-performance streaming
	pipelineDepth = 4               // chunks in flight for async I/O
	writerBufSize = 4 * 1024 * 1024 // 4MB bufio buffer for output
)

var (
	ErrInvalidMagic   = errors.New("invalid file format: not an encrypted xgrabba archive")
	ErrInvalidVersion = errors.New("unsupported encryption format version")
	ErrDecryptFailed  = errors.New("decryption failed: wrong password or corrupted data")
	ErrCancelled      = errors.New("encryption cancelled")
)

// V2Header describes the streaming encryption header.
type V2Header struct {
	Salt      []byte
	BaseNonce []byte
	ChunkSize uint32
}

// ReadV2HeaderAt reads and validates a v2 header from a ReaderAt.
func ReadV2HeaderAt(r io.ReaderAt) (V2Header, error) {
	header := make([]byte, HeaderSizeV2)
	n, err := r.ReadAt(header, 0)
	if err != nil && n != len(header) {
		return V2Header{}, fmt.Errorf("read header: %w", err)
	}
	if string(header[0:4]) != MagicBytes {
		return V2Header{}, ErrInvalidMagic
	}
	version := binary.LittleEndian.Uint32(header[4:8])
	if version != FormatVersionV2 {
		return V2Header{}, ErrInvalidVersion
	}
	salt := make([]byte, SaltSize)
	baseNonce := make([]byte, NonceSize)
	copy(salt, header[8:8+SaltSize])
	copy(baseNonce, header[8+SaltSize:8+SaltSize+NonceSize])
	chunkSize := binary.LittleEndian.Uint32(header[8+SaltSize+NonceSize:])
	return V2Header{
		Salt:      salt,
		BaseNonce: baseNonce,
		ChunkSize: chunkSize,
	}, nil
}

// DecryptChunkAt decrypts a single chunk by index from a v2-encrypted file.
// expectedPlainLen should be the plain chunk length (useful for validating last chunk).
func DecryptChunkAt(r io.ReaderAt, key []byte, header V2Header, chunkIndex int, expectedPlainLen int) ([]byte, error) {
	if chunkIndex < 0 {
		return nil, fmt.Errorf("invalid chunk index: %d", chunkIndex)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	chunkSize := int64(header.ChunkSize)
	chunkOffset := int64(HeaderSizeV2) + int64(chunkIndex)*(4+chunkSize+GCMTagSize)
	lenBuf := make([]byte, 4)
	if _, err := r.ReadAt(lenBuf, chunkOffset); err != nil {
		return nil, fmt.Errorf("read chunk length: %w", err)
	}
	plainLen := int(binary.LittleEndian.Uint32(lenBuf))
	if plainLen == 0 {
		return nil, fmt.Errorf("unexpected end marker at chunk %d", chunkIndex)
	}
	if plainLen > int(header.ChunkSize) {
		return nil, fmt.Errorf("invalid chunk length: %d > %d", plainLen, header.ChunkSize)
	}
	if expectedPlainLen > 0 && plainLen != expectedPlainLen {
		return nil, fmt.Errorf("unexpected chunk length: got %d expected %d", plainLen, expectedPlainLen)
	}

	ciphertextLen := plainLen + GCMTagSize
	ciphertext := make([]byte, ciphertextLen)
	if _, err := r.ReadAt(ciphertext, chunkOffset+4); err != nil {
		return nil, fmt.Errorf("read ciphertext: %w", err)
	}

	chunkNonce := make([]byte, NonceSize)
	copy(chunkNonce, header.BaseNonce)
	counterBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(counterBytes, uint64(chunkIndex))
	for i := 0; i < 8 && i < NonceSize; i++ {
		chunkNonce[i] ^= counterBytes[i]
	}

	plaintext, err := gcm.Open(nil, chunkNonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}

	return plaintext, nil
}

// chunkPool provides reusable chunk buffers to reduce allocation pressure
// when encrypting multiple files sequentially.
var chunkPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, DefaultChunkSize)
		return &buf
	},
}

// encryptedChunk represents an encrypted chunk ready for writing.
type encryptedChunk struct {
	lengthBuf  []byte // 4-byte length header
	ciphertext []byte // encrypted data including GCM tag
}

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

// Encrypt encrypts data using the pre-derived key (v1 format).
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

// EncryptStream encrypts data from reader to writer using chunked GCM (v2 format).
// This enables streaming encryption with constant memory usage regardless of file size.
// Each chunk is individually authenticated with GCM, using a unique nonce derived from
// the base nonce XORed with the chunk counter.
func (e *Encryptor) EncryptStream(reader io.Reader, writer io.Writer) (int64, error) {
	return e.EncryptStreamWithContext(context.Background(), reader, writer)
}

// EncryptStreamWithContext encrypts data with cancellation support.
func (e *Encryptor) EncryptStreamWithContext(ctx context.Context, reader io.Reader, writer io.Writer) (int64, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return 0, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, fmt.Errorf("create GCM: %w", err)
	}

	// Generate random base nonce
	baseNonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, baseNonce); err != nil {
		return 0, fmt.Errorf("generate nonce: %w", err)
	}

	// Write v2 header: magic + version + salt + nonce + chunkSize
	header := make([]byte, HeaderSizeV2)
	copy(header[0:4], MagicBytes)
	binary.LittleEndian.PutUint32(header[4:8], FormatVersionV2)
	copy(header[8:8+SaltSize], e.salt)
	copy(header[8+SaltSize:8+SaltSize+NonceSize], baseNonce)
	binary.LittleEndian.PutUint32(header[8+SaltSize+NonceSize:], DefaultChunkSize)

	if _, err := writer.Write(header); err != nil {
		return 0, fmt.Errorf("write header: %w", err)
	}

	var totalWritten int64

	// Get chunk buffer from pool to reduce allocations
	chunkBufPtr := chunkPool.Get().(*[]byte)
	chunkBuf := *chunkBufPtr
	defer chunkPool.Put(chunkBufPtr)

	// Pre-allocate reusable buffers outside the hot loop
	chunkNonce := make([]byte, NonceSize)
	counterBytes := make([]byte, 8)
	lenBuf := make([]byte, 4)

	// Pre-allocate ciphertext buffer (chunk + GCM tag) to avoid allocation per chunk
	ciphertextBuf := make([]byte, 0, DefaultChunkSize+GCMTagSize)

	chunkNum := uint64(0)

	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return totalWritten, fmt.Errorf("%w: %v", ErrCancelled, ctx.Err())
		default:
		}

		// Read chunk
		n, err := io.ReadFull(reader, chunkBuf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return totalWritten, fmt.Errorf("read chunk: %w", err)
		}

		if n == 0 {
			break // End of file
		}

		// Derive unique nonce for this chunk by XORing with chunk counter
		copy(chunkNonce, baseNonce)
		binary.LittleEndian.PutUint64(counterBytes, chunkNum)
		for i := 0; i < 8; i++ {
			chunkNonce[i] ^= counterBytes[i]
		}

		// Write chunk length (4 bytes, little-endian)
		binary.LittleEndian.PutUint32(lenBuf, uint32(n))
		if _, err := writer.Write(lenBuf); err != nil {
			return totalWritten, fmt.Errorf("write chunk length: %w", err)
		}

		// Encrypt and write chunk (ciphertext includes GCM tag)
		// Reuse ciphertextBuf to avoid allocation
		ciphertextBuf = gcm.Seal(ciphertextBuf[:0], chunkNonce, chunkBuf[:n], nil)
		if _, err := writer.Write(ciphertextBuf); err != nil {
			return totalWritten, fmt.Errorf("write ciphertext: %w", err)
		}

		totalWritten += int64(n)
		chunkNum++

		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
	}

	// Write end marker (zero-length chunk)
	endMarker := make([]byte, 4)
	if _, err := writer.Write(endMarker); err != nil {
		return totalWritten, fmt.Errorf("write end marker: %w", err)
	}

	return totalWritten, nil
}

// DecryptStream decrypts v2 chunked data from reader to writer.
func DecryptStream(reader io.Reader, writer io.Writer, password string) (int64, error) {
	return DecryptStreamWithContext(context.Background(), reader, writer, password)
}

// DecryptStreamWithContext decrypts v2 chunked data with cancellation support.
func DecryptStreamWithContext(ctx context.Context, reader io.Reader, writer io.Writer, password string) (int64, error) {
	// Read header
	header := make([]byte, HeaderSizeV2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, fmt.Errorf("read header: %w", err)
	}

	// Verify magic
	if string(header[0:4]) != MagicBytes {
		return 0, ErrInvalidMagic
	}

	// Check version
	version := binary.LittleEndian.Uint32(header[4:8])
	if version != FormatVersionV2 {
		return 0, ErrInvalidVersion
	}

	// Extract salt, nonce, and chunk size
	salt := header[8 : 8+SaltSize]
	baseNonce := header[8+SaltSize : 8+SaltSize+NonceSize]
	chunkSize := binary.LittleEndian.Uint32(header[8+SaltSize+NonceSize:])

	// Derive key
	key := DeriveKey(password, salt)

	// Create cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return 0, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, fmt.Errorf("create GCM: %w", err)
	}

	var totalRead int64
	chunkNonce := make([]byte, NonceSize)
	chunkNum := uint64(0)
	lenBuf := make([]byte, 4)

	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return totalRead, fmt.Errorf("%w: %v", ErrCancelled, ctx.Err())
		default:
		}

		// Read chunk length
		if _, err := io.ReadFull(reader, lenBuf); err != nil {
			if err == io.EOF {
				break
			}
			return totalRead, fmt.Errorf("read chunk length: %w", err)
		}

		plainLen := binary.LittleEndian.Uint32(lenBuf)
		if plainLen == 0 {
			break // End marker
		}

		if plainLen > chunkSize {
			return totalRead, fmt.Errorf("invalid chunk length: %d > %d", plainLen, chunkSize)
		}

		// Derive unique nonce for this chunk
		copy(chunkNonce, baseNonce)
		counterBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(counterBytes, chunkNum)
		for i := 0; i < 8 && i < NonceSize; i++ {
			chunkNonce[i] ^= counterBytes[i]
		}

		// Read ciphertext (plainLen + GCM tag)
		ciphertextLen := plainLen + GCMTagSize
		ciphertext := make([]byte, ciphertextLen)
		if _, err := io.ReadFull(reader, ciphertext); err != nil {
			return totalRead, fmt.Errorf("read ciphertext: %w", err)
		}

		// Decrypt chunk
		plaintext, err := gcm.Open(nil, chunkNonce, ciphertext, nil)
		if err != nil {
			return totalRead, ErrDecryptFailed
		}

		// Write plaintext
		if _, err := writer.Write(plaintext); err != nil {
			return totalRead, fmt.Errorf("write plaintext: %w", err)
		}

		totalRead += int64(len(plaintext))
		chunkNum++
	}

	return totalRead, nil
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

// Decrypt decrypts data that was encrypted with Encrypt (v1) or EncryptStream (v2).
// Automatically detects the format version and handles accordingly.
func Decrypt(data []byte, password string) ([]byte, error) {
	if len(data) < HeaderSize {
		return nil, ErrInvalidMagic
	}

	// Verify magic bytes
	if string(data[0:4]) != MagicBytes {
		return nil, ErrInvalidMagic
	}

	// Check version and dispatch to appropriate handler
	version := binary.LittleEndian.Uint32(data[4:8])
	switch version {
	case FormatVersion:
		return decryptV1(data, password)
	case FormatVersionV2:
		return decryptV2(data, password)
	default:
		return nil, ErrInvalidVersion
	}
}

// decryptV1 handles legacy full-file encryption format.
func decryptV1(data []byte, password string) ([]byte, error) {
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

// decryptV2 handles chunked streaming format (in-memory for small files).
func decryptV2(data []byte, password string) ([]byte, error) {
	if len(data) < HeaderSizeV2 {
		return nil, ErrInvalidMagic
	}

	// Extract salt, nonce, and chunk size
	salt := data[8 : 8+SaltSize]
	baseNonce := data[8+SaltSize : 8+SaltSize+NonceSize]
	chunkSize := binary.LittleEndian.Uint32(data[8+SaltSize+NonceSize:])

	// Derive key
	key := DeriveKey(password, salt)

	// Create cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	// Decrypt chunks
	var result []byte
	chunkNonce := make([]byte, NonceSize)
	chunkNum := uint64(0)
	pos := HeaderSizeV2

	for pos < len(data) {
		// Read chunk length
		if pos+4 > len(data) {
			return nil, fmt.Errorf("truncated chunk header at position %d", pos)
		}
		plainLen := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4

		if plainLen == 0 {
			break // End marker
		}

		if plainLen > chunkSize {
			return nil, fmt.Errorf("invalid chunk length: %d > %d", plainLen, chunkSize)
		}

		// Derive unique nonce for this chunk
		copy(chunkNonce, baseNonce)
		counterBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(counterBytes, chunkNum)
		for i := 0; i < 8 && i < NonceSize; i++ {
			chunkNonce[i] ^= counterBytes[i]
		}

		// Read ciphertext
		ciphertextLen := int(plainLen) + GCMTagSize
		if pos+ciphertextLen > len(data) {
			return nil, fmt.Errorf("truncated ciphertext at position %d", pos)
		}
		ciphertext := data[pos : pos+ciphertextLen]
		pos += ciphertextLen

		// Decrypt chunk
		plaintext, err := gcm.Open(nil, chunkNonce, ciphertext, nil)
		if err != nil {
			return nil, ErrDecryptFailed
		}

		result = append(result, plaintext...)
		chunkNum++
	}

	return result, nil
}

// EncryptFile encrypts a file and writes it to the destination (v1 format, in-memory).
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

// EncryptFileStream encrypts a file using streaming (v2 format, constant memory).
// This is the recommended method for large files as it doesn't load the entire file into memory.
// Uses buffered I/O to coalesce small writes (4-byte length headers) for better throughput.
func EncryptFileStream(ctx context.Context, srcPath, dstPath string, enc *Encryptor) (int64, error) {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return 0, fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return 0, fmt.Errorf("create destination: %w", err)
	}

	// Wrap destination in buffered writer to coalesce small writes
	// This significantly improves throughput by batching the 4-byte length headers
	// with the ciphertext writes instead of issuing separate syscalls
	bufWriter := bufio.NewWriterSize(dstFile, writerBufSize)

	written, err := enc.EncryptStreamWithContext(ctx, srcFile, bufWriter)
	if err != nil {
		dstFile.Close()
		os.Remove(dstPath) // Clean up partial file
		return written, err
	}

	// Flush buffered writer before sync
	if err := bufWriter.Flush(); err != nil {
		dstFile.Close()
		os.Remove(dstPath)
		return written, fmt.Errorf("flush buffer: %w", err)
	}

	// Sync to ensure data is written to disk (critical for USB drives)
	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		return written, fmt.Errorf("sync destination: %w", err)
	}

	if err := dstFile.Close(); err != nil {
		return written, fmt.Errorf("close destination: %w", err)
	}

	return written, nil
}

// DecryptFileStream decrypts a file using streaming (v2 format).
func DecryptFileStream(ctx context.Context, srcPath, dstPath, password string) (int64, error) {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return 0, fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return 0, fmt.Errorf("create destination: %w", err)
	}

	written, err := DecryptStreamWithContext(ctx, srcFile, dstFile, password)
	if err != nil {
		dstFile.Close()
		os.Remove(dstPath) // Clean up partial file
		return written, err
	}

	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		return written, fmt.Errorf("sync destination: %w", err)
	}

	if err := dstFile.Close(); err != nil {
		return written, fmt.Errorf("close destination: %w", err)
	}

	return written, nil
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
	SourcePath string
	DestPath   string
	RelPath    string // Relative path for manifest
	EncName    string // Encrypted filename
}

// EncryptionResult represents the result of an encryption job
type EncryptionResult struct {
	Job   EncryptionJob
	Error error
}

// ParallelEncryptor encrypts multiple files in parallel using a shared key.
type ParallelEncryptor struct {
	encryptor  *Encryptor
	workers    int
	progressFn func(completed, total int, currentFile string)
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
	var errs []error
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
					errs = append(errs, fmt.Errorf("read %s: %w", job.SourcePath, err))
					errorsMu.Unlock()
					continue
				}

				// Encrypt
				encrypted, err := p.encryptor.Encrypt(data)
				if err != nil {
					errorsMu.Lock()
					errs = append(errs, fmt.Errorf("encrypt %s: %w", job.SourcePath, err))
					errorsMu.Unlock()
					continue
				}

				// Write encrypted file
				if err := os.WriteFile(job.DestPath, encrypted, 0644); err != nil {
					errorsMu.Lock()
					errs = append(errs, fmt.Errorf("write %s: %w", job.DestPath, err))
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

	return manifest, errs
}
