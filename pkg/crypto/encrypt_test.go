package crypto

import (
	"bytes"
	"context"
	"encoding/binary"
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

func TestDecryptChunkAt(t *testing.T) {
	password := "chunk-test!"
	plaintext := make([]byte, DefaultChunkSize+123)
	for i := range plaintext {
		plaintext[i] = byte(i % 251)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	var encrypted bytes.Buffer
	_, err = enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
	if err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	reader := bytes.NewReader(encrypted.Bytes())
	header, err := ReadV2HeaderAt(reader)
	if err != nil {
		t.Fatalf("ReadV2HeaderAt failed: %v", err)
	}
	key := DeriveKey(password, header.Salt)

	chunk0, err := DecryptChunkAt(reader, key, header, 0, DefaultChunkSize)
	if err != nil {
		t.Fatalf("DecryptChunkAt chunk 0 failed: %v", err)
	}
	if !bytes.Equal(chunk0, plaintext[:DefaultChunkSize]) {
		t.Fatalf("chunk 0 mismatch")
	}

	lastLen := len(plaintext) - DefaultChunkSize
	chunk1, err := DecryptChunkAt(reader, key, header, 1, lastLen)
	if err != nil {
		t.Fatalf("DecryptChunkAt chunk 1 failed: %v", err)
	}
	if !bytes.Equal(chunk1, plaintext[DefaultChunkSize:]) {
		t.Fatalf("chunk 1 mismatch")
	}
}

func TestReadV2HeaderAt_InvalidMagic(t *testing.T) {
	data := make([]byte, HeaderSizeV2)
	copy(data[0:4], []byte("NOPE"))
	reader := bytes.NewReader(data)

	_, err := ReadV2HeaderAt(reader)
	if err == nil || !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}
}

func TestReadV2HeaderAt_InvalidVersion(t *testing.T) {
	data := make([]byte, HeaderSizeV2)
	copy(data[0:4], MagicBytes)
	binary.LittleEndian.PutUint32(data[4:8], FormatVersion) // v1
	reader := bytes.NewReader(data)

	_, err := ReadV2HeaderAt(reader)
	if err == nil || !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("expected ErrInvalidVersion, got %v", err)
	}
}

func TestDecryptChunkAt_WrongKey(t *testing.T) {
	password := "chunk-test!"
	plaintext := make([]byte, DefaultChunkSize)
	for i := range plaintext {
		plaintext[i] = byte(i % 251)
	}

	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	var encrypted bytes.Buffer
	_, err = enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
	if err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	reader := bytes.NewReader(encrypted.Bytes())
	header, err := ReadV2HeaderAt(reader)
	if err != nil {
		t.Fatalf("ReadV2HeaderAt failed: %v", err)
	}
	wrongKey := DeriveKey("wrong-password", header.Salt)

	_, err = DecryptChunkAt(reader, wrongKey, header, 0, DefaultChunkSize)
	if err == nil || !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("expected ErrDecryptFailed, got %v", err)
	}
}

func TestDecryptChunkAt_InvalidIndex(t *testing.T) {
	password := "chunk-test!"
	plaintext := make([]byte, DefaultChunkSize)
	enc, err := NewEncryptor(password)
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	var encrypted bytes.Buffer
	_, err = enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)
	if err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	reader := bytes.NewReader(encrypted.Bytes())
	header, err := ReadV2HeaderAt(reader)
	if err != nil {
		t.Fatalf("ReadV2HeaderAt failed: %v", err)
	}
	key := DeriveKey(password, header.Salt)

	_, err = DecryptChunkAt(reader, key, header, -1, DefaultChunkSize)
	if err == nil {
		t.Fatalf("expected error for invalid chunk index")
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

func TestEncryptor_Encrypt_EmptyData(t *testing.T) {
	enc, err := NewEncryptor("password")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	ciphertext, err := enc.Encrypt([]byte{})
	if err != nil {
		t.Fatalf("Encrypt empty data failed: %v", err)
	}

	decrypted, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if len(decrypted) != 0 {
		t.Error("decrypted empty data should be empty")
	}
}

func TestEncryptor_Decrypt_InvalidVersion(t *testing.T) {
	enc, _ := NewEncryptor("password")
	ciphertext, _ := enc.Encrypt([]byte("test"))

	// Corrupt version
	binary.LittleEndian.PutUint32(ciphertext[4:8], 999)

	_, err := enc.Decrypt(ciphertext)
	if err != ErrInvalidVersion {
		t.Errorf("expected ErrInvalidVersion, got %v", err)
	}
}

func TestEncryptor_Decrypt_TooShort(t *testing.T) {
	enc, _ := NewEncryptor("password")
	_, err := enc.Decrypt([]byte("short"))
	if err != ErrInvalidMagic {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}
}

func TestEncryptor_Salt(t *testing.T) {
	enc, err := NewEncryptor("password")
	if err != nil {
		t.Fatalf("NewEncryptor failed: %v", err)
	}

	salt := enc.Salt()
	if len(salt) != SaltSize {
		t.Errorf("salt length = %d, want %d", len(salt), SaltSize)
	}
}

func TestNewEncryptorWithSalt(t *testing.T) {
	salt, _ := GenerateSalt()
	enc := NewEncryptorWithSalt("password", salt)

	if enc == nil {
		t.Fatal("NewEncryptorWithSalt returned nil")
	}

	if !bytes.Equal(enc.Salt(), salt) {
		t.Error("salt should match provided salt")
	}
}

func TestDecrypt_InvalidVersion(t *testing.T) {
	plaintext := []byte("test")
	ciphertext, _ := Encrypt(plaintext, "password")

	// Corrupt version to unsupported value
	binary.LittleEndian.PutUint32(ciphertext[4:8], 999)

	_, err := Decrypt(ciphertext, "password")
	if err != ErrInvalidVersion {
		t.Errorf("expected ErrInvalidVersion, got %v", err)
	}
}

func TestDecryptV2_TruncatedData(t *testing.T) {
	password := "test"
	plaintext := []byte("test data")
	enc, _ := NewEncryptor(password)

	var encrypted bytes.Buffer
	enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)

	// Truncate data
	truncated := encrypted.Bytes()[:len(encrypted.Bytes())-10]

	_, err := Decrypt(truncated, password)
	if err == nil {
		t.Error("expected error for truncated data")
	}
}

func TestDecryptV2_InvalidChunkLength(t *testing.T) {
	password := "test"
	plaintext := []byte("test")
	enc, _ := NewEncryptor(password)

	var encrypted bytes.Buffer
	enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)

	encBytes := encrypted.Bytes()
	// Corrupt chunk length to be larger than chunk size
	pos := HeaderSizeV2
	binary.LittleEndian.PutUint32(encBytes[pos:pos+4], DefaultChunkSize+1)

	_, err := Decrypt(encBytes, password)
	if err == nil {
		t.Error("expected error for invalid chunk length")
	}
}

func TestIsEncryptedFile_NotExists(t *testing.T) {
	if IsEncryptedFile("/nonexistent/file/path") {
		t.Error("IsEncryptedFile should return false for non-existent file")
	}
}

func TestIsEncryptedFile_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	emptyPath := filepath.Join(tmpDir, "empty")
	os.WriteFile(emptyPath, []byte{}, 0644)

	if IsEncryptedFile(emptyPath) {
		t.Error("IsEncryptedFile should return false for empty file")
	}
}

func TestEncryptFile_NotExists(t *testing.T) {
	err := EncryptFile("/nonexistent", "/tmp/out", "password")
	if err == nil {
		t.Error("expected error for non-existent source file")
	}
}

func TestEncryptFileStream_NotExists(t *testing.T) {
	enc, _ := NewEncryptor("password")
	_, err := EncryptFileStream(context.Background(), "/nonexistent", "/tmp/out", enc)
	if err == nil {
		t.Error("expected error for non-existent source file")
	}
}

func TestDecryptFileStream_NotExists(t *testing.T) {
	_, err := DecryptFileStream(context.Background(), "/nonexistent", "/tmp/out", "password")
	if err == nil {
		t.Error("expected error for non-existent source file")
	}
}

func TestDecryptFile_NotExists(t *testing.T) {
	_, err := DecryptFile("/nonexistent", "password")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestDecryptStream_ContextCanceled(t *testing.T) {
	password := "test"
	plaintext := make([]byte, DefaultChunkSize*2)
	enc, _ := NewEncryptor(password)

	var encrypted bytes.Buffer
	enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var decrypted bytes.Buffer
	_, err := DecryptStreamWithContext(ctx, bytes.NewReader(encrypted.Bytes()), &decrypted, password)
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestParallelEncryptor_EmptyJobs(t *testing.T) {
	pe, err := NewParallelEncryptor("password", 4, nil)
	if err != nil {
		t.Fatalf("NewParallelEncryptor failed: %v", err)
	}

	manifest, errs := pe.EncryptFiles([]EncryptionJob{})
	if len(manifest) != 0 {
		t.Error("manifest should be empty for no jobs")
	}
	if len(errs) != 0 {
		t.Error("errors should be empty for no jobs")
	}
}

func TestParallelEncryptor_InvalidWorkers(t *testing.T) {
	pe, err := NewParallelEncryptor("password", 0, nil)
	if err != nil {
		t.Fatalf("NewParallelEncryptor failed: %v", err)
	}

	if pe.workers != 4 {
		t.Errorf("workers = %d, want 4 (default)", pe.workers)
	}
}

func TestParallelEncryptor_EncryptFiles_Error(t *testing.T) {
	pe, _ := NewParallelEncryptor("password", 2, nil)

	tmpDir := t.TempDir()
	jobs := []EncryptionJob{
		{
			SourcePath: "/nonexistent/file",
			DestPath:   filepath.Join(tmpDir, "out.enc"),
			RelPath:    "test",
			EncName:    "out.enc",
		},
	}

	manifest, errs := pe.EncryptFiles(jobs)
	if len(errs) == 0 {
		t.Error("expected errors for non-existent files")
	}
	if len(manifest) != 0 {
		t.Error("manifest should be empty when all jobs fail")
	}
}

func TestReadV2HeaderAt_ShortData(t *testing.T) {
	reader := bytes.NewReader([]byte("XGC"))
	_, err := ReadV2HeaderAt(reader)
	if err == nil {
		t.Error("expected error for short data")
	}
}

func TestDecryptChunkAt_EndMarker(t *testing.T) {
	password := "test"
	plaintext := []byte("small")
	enc, _ := NewEncryptor(password)

	var encrypted bytes.Buffer
	enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)

	reader := bytes.NewReader(encrypted.Bytes())
	header, _ := ReadV2HeaderAt(reader)
	key := DeriveKey(password, header.Salt)

	// Try to read past end (chunk 1 should be end marker)
	_, err := DecryptChunkAt(reader, key, header, 1, 0)
	if err == nil {
		t.Error("expected error for end marker chunk")
	}
}

func TestDecryptChunkAt_InvalidLength(t *testing.T) {
	password := "test"
	plaintext := make([]byte, DefaultChunkSize)
	enc, _ := NewEncryptor(password)

	var encrypted bytes.Buffer
	enc.EncryptStream(bytes.NewReader(plaintext), &encrypted)

	reader := bytes.NewReader(encrypted.Bytes())
	header, _ := ReadV2HeaderAt(reader)
	key := DeriveKey(password, header.Salt)

	// Request with wrong expected length
	_, err := DecryptChunkAt(reader, key, header, 0, DefaultChunkSize+100)
	if err == nil {
		t.Error("expected error for length mismatch")
	}
}
