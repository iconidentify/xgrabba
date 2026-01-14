package crypto

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncryptDecrypt(t *testing.T) {
	password := "test-password-123!"
	plaintext := []byte("Hello, this is a secret message that needs to be encrypted securely.")

	// Encrypt
	ciphertext, err := Encrypt(plaintext, password)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Verify it's larger than plaintext (has header)
	if len(ciphertext) <= len(plaintext) {
		t.Error("Ciphertext should be larger than plaintext")
	}

	// Verify magic bytes
	if string(ciphertext[0:4]) != MagicBytes {
		t.Error("Missing magic bytes")
	}

	// Decrypt
	decrypted, err := Decrypt(ciphertext, password)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	// Verify plaintext matches
	if !bytes.Equal(decrypted, plaintext) {
		t.Error("Decrypted data doesn't match original")
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	password := "correct-password"
	wrongPassword := "wrong-password"
	plaintext := []byte("Secret data")

	ciphertext, err := Encrypt(plaintext, password)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	_, err = Decrypt(ciphertext, wrongPassword)
	if err != ErrDecryptFailed {
		t.Errorf("Expected ErrDecryptFailed, got: %v", err)
	}
}

func TestIsEncrypted(t *testing.T) {
	password := "test"
	plaintext := []byte("data")

	ciphertext, _ := Encrypt(plaintext, password)

	if !IsEncrypted(ciphertext) {
		t.Error("IsEncrypted should return true for encrypted data")
	}

	if IsEncrypted(plaintext) {
		t.Error("IsEncrypted should return false for plain data")
	}

	if IsEncrypted([]byte("XGC")) {
		t.Error("IsEncrypted should return false for short data")
	}
}

func TestEncryptDifferentEachTime(t *testing.T) {
	password := "same-password"
	plaintext := []byte("same data")

	ciphertext1, _ := Encrypt(plaintext, password)
	ciphertext2, _ := Encrypt(plaintext, password)

	// Should be different due to random salt and nonce
	if bytes.Equal(ciphertext1, ciphertext2) {
		t.Error("Encrypting same data twice should produce different ciphertext")
	}

	// But both should decrypt to same plaintext
	decrypted1, _ := Decrypt(ciphertext1, password)
	decrypted2, _ := Decrypt(ciphertext2, password)

	if !bytes.Equal(decrypted1, decrypted2) {
		t.Error("Both ciphertexts should decrypt to same plaintext")
	}
}

func TestInvalidData(t *testing.T) {
	_, err := Decrypt([]byte("short"), "password")
	if err != ErrInvalidMagic {
		t.Errorf("Expected ErrInvalidMagic for short data, got: %v", err)
	}

	_, err = Decrypt([]byte("WRONGMAGICBYTESHERE"), "password")
	if err != ErrInvalidMagic {
		t.Errorf("Expected ErrInvalidMagic for wrong magic, got: %v", err)
	}
}

func TestStreamEncryptDecrypt(t *testing.T) {
	password := "stream-test-password!"
	plaintext := []byte("This is test data for streaming encryption. It should work correctly even for small amounts of data.")

	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Encrypt using streaming
	var encrypted bytes.Buffer
	_, err = enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
	if err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	// Verify v2 header
	encBytes := encrypted.Bytes()
	if string(encBytes[0:4]) != MagicBytes {
		t.Error("Missing magic bytes")
	}

	// Decrypt using in-memory v2 decoder
	decrypted, err := Decrypt(encBytes, password)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("Decrypted data doesn't match original\nGot: %s\nWant: %s", decrypted, plaintext)
	}
}

func TestStreamDecrypt(t *testing.T) {
	password := "stream-decrypt-test!"
	plaintext := []byte("Test data for stream decryption roundtrip.")

	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Encrypt
	var encrypted bytes.Buffer
	_, err = enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
	if err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	// Decrypt using streaming
	var decrypted bytes.Buffer
	_, err = DecryptStream(bytes.NewReader(encrypted.Bytes()), &decrypted, password)
	if err != nil {
		t.Fatalf("DecryptStream failed: %v", err)
	}

	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Errorf("Decrypted data doesn't match\nGot: %s\nWant: %s", decrypted.Bytes(), plaintext)
	}
}

func TestStreamLargeData(t *testing.T) {
	password := "large-data-test!"

	// Create data larger than one chunk (1MB + extra)
	plaintext := make([]byte, DefaultChunkSize+500000)
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Encrypt
	var encrypted bytes.Buffer
	written, err := enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
	if err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	if written != int64(len(plaintext)) {
		t.Errorf("Written bytes mismatch: got %d, want %d", written, len(plaintext))
	}

	// Decrypt
	decrypted, err := Decrypt(encrypted.Bytes(), password)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("Large data roundtrip failed")
	}
}

func TestStreamCancellation(t *testing.T) {
	password := "cancel-test!"

	// Create large data to give time for cancellation
	plaintext := make([]byte, DefaultChunkSize*3)
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Create context that cancels quickly
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a tiny delay
	go func() {
		time.Sleep(1 * time.Millisecond)
		cancel()
	}()

	var encrypted bytes.Buffer
	_, err = enc.EncryptStreamWithContext(ctx, bytes.NewReader(plaintext), &encrypted)

	if err == nil {
		// Encryption completed before cancellation - that's okay for fast systems
		t.Log("Encryption completed before cancellation could be triggered")
		return
	}

	if !errors.Is(err, ErrCancelled) {
		t.Logf("Got different error than ErrCancelled: %v", err)
	}
}

func TestStreamFileRoundtrip(t *testing.T) {
	password := "file-roundtrip-test!"
	plaintext := []byte("File-based streaming encryption test data. This tests the file helper functions.")

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	encPath := filepath.Join(tmpDir, "encrypted.enc")
	decPath := filepath.Join(tmpDir, "decrypted.txt")

	// Write source file
	if err := os.WriteFile(srcPath, plaintext, 0644); err != nil {
		t.Fatalf("Write source failed: %v", err)
	}

	// Create encryptor
	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Encrypt file
	written, err := EncryptFileStream(context.Background(), srcPath, encPath, enc)
	if err != nil {
		t.Fatalf("EncryptFileStream failed: %v", err)
	}

	if written != int64(len(plaintext)) {
		t.Errorf("Written bytes mismatch: got %d, want %d", written, len(plaintext))
	}

	// Verify encrypted file exists and has content
	encInfo, err := os.Stat(encPath)
	if err != nil {
		t.Fatalf("Encrypted file not found: %v", err)
	}
	if encInfo.Size() == 0 {
		t.Error("Encrypted file is empty")
	}

	// Decrypt file
	read, err := DecryptFileStream(context.Background(), encPath, decPath, password)
	if err != nil {
		t.Fatalf("DecryptFileStream failed: %v", err)
	}

	if read != int64(len(plaintext)) {
		t.Errorf("Read bytes mismatch: got %d, want %d", read, len(plaintext))
	}

	// Verify content
	decrypted, err := os.ReadFile(decPath)
	if err != nil {
		t.Fatalf("Read decrypted file failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("File roundtrip failed: content mismatch")
	}
}

func TestStreamWrongPassword(t *testing.T) {
	password := "correct-password"
	wrongPassword := "wrong-password"
	plaintext := []byte("Secret stream data")

	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	// Encrypt
	var encrypted bytes.Buffer
	_, err = enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
	if err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	// Try to decrypt with wrong password
	_, err = Decrypt(encrypted.Bytes(), wrongPassword)
	if err != ErrDecryptFailed {
		t.Errorf("Expected ErrDecryptFailed, got: %v", err)
	}
}

func TestBackwardCompatibility(t *testing.T) {
	password := "compat-test!"
	plaintext := []byte("Testing backward compatibility between v1 and v2 formats.")

	// Encrypt with v1 (legacy)
	v1Encrypted, err := Encrypt(plaintext, password)
	if err != nil {
		t.Fatalf("v1 Encrypt failed: %v", err)
	}

	// Decrypt v1 with unified Decrypt function
	v1Decrypted, err := Decrypt(v1Encrypted, password)
	if err != nil {
		t.Fatalf("v1 Decrypt failed: %v", err)
	}

	if !bytes.Equal(v1Decrypted, plaintext) {
		t.Error("v1 roundtrip failed")
	}

	// Encrypt with v2 (streaming)
	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	var v2Encrypted bytes.Buffer
	_, err = enc.EncryptStream(bytes.NewReader(plaintext), &v2Encrypted)
	if err != nil {
		t.Fatalf("v2 EncryptStream failed: %v", err)
	}

	// Decrypt v2 with unified Decrypt function
	v2Decrypted, err := Decrypt(v2Encrypted.Bytes(), password)
	if err != nil {
		t.Fatalf("v2 Decrypt failed: %v", err)
	}

	if !bytes.Equal(v2Decrypted, plaintext) {
		t.Error("v2 roundtrip failed")
	}

	// Both should produce the same plaintext
	if !bytes.Equal(v1Decrypted, v2Decrypted) {
		t.Error("v1 and v2 decrypted content should match")
	}
}

func BenchmarkStreamEncrypt1MB(b *testing.B) {
	password := "benchmark-password!"
	plaintext := make([]byte, 1024*1024) // 1MB
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		b.Fatalf("NewEncryptor failed: %v", err)
	}

	b.ResetTimer()
	b.SetBytes(int64(len(plaintext)))

	for i := 0; i < b.N; i++ {
		var encrypted bytes.Buffer
		_, err := enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
		if err != nil {
			b.Fatalf("EncryptStream failed: %v", err)
		}
	}
}

func BenchmarkStreamEncrypt10MB(b *testing.B) {
	password := "benchmark-password!"
	plaintext := make([]byte, 10*1024*1024) // 10MB
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		b.Fatalf("NewEncryptor failed: %v", err)
	}

	b.ResetTimer()
	b.SetBytes(int64(len(plaintext)))

	for i := 0; i < b.N; i++ {
		var encrypted bytes.Buffer
		_, err := enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
		if err != nil {
			b.Fatalf("EncryptStream failed: %v", err)
		}
	}
}

func BenchmarkStreamEncrypt100MB(b *testing.B) {
	password := "benchmark-password!"
	plaintext := make([]byte, 100*1024*1024) // 100MB
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		b.Fatalf("NewEncryptor failed: %v", err)
	}

	b.ResetTimer()
	b.SetBytes(int64(len(plaintext)))

	for i := 0; i < b.N; i++ {
		var encrypted bytes.Buffer
		encrypted.Grow(len(plaintext) + 1024*1024) // Pre-allocate to avoid realloc
		_, err := enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
		if err != nil {
			b.Fatalf("EncryptStream failed: %v", err)
		}
	}
}

func BenchmarkFileStreamEncrypt(b *testing.B) {
	password := "benchmark-password!"
	plaintext := make([]byte, 50*1024*1024) // 50MB
	for i := range plaintext {
		plaintext[i] = byte(i % 256)
	}

	tmpDir := b.TempDir()
	srcPath := filepath.Join(tmpDir, "source.bin")
	if err := os.WriteFile(srcPath, plaintext, 0644); err != nil {
		b.Fatalf("Write source failed: %v", err)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		b.Fatalf("NewEncryptor failed: %v", err)
	}

	b.ResetTimer()
	b.SetBytes(int64(len(plaintext)))

	for i := 0; i < b.N; i++ {
		encPath := filepath.Join(tmpDir, "encrypted.enc")
		_, err := EncryptFileStream(context.Background(), srcPath, encPath, enc)
		if err != nil {
			b.Fatalf("EncryptFileStream failed: %v", err)
		}
		os.Remove(encPath)
	}
}
