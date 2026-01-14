package crypto

import (
	"bytes"
	"testing"
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
