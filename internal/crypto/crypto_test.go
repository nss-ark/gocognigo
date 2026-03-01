package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	original := "sk-abc123def456ghi789"
	encrypted, err := Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}

	if decrypted != original {
		t.Errorf("roundtrip failed: got %q, want %q", decrypted, original)
	}
}

func TestEncryptDecrypt_EmptyString(t *testing.T) {
	encrypted, err := Encrypt("")
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if encrypted != "" {
		t.Errorf("expected empty string for empty input, got %q", encrypted)
	}

	decrypted, err := Decrypt("")
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}
	if decrypted != "" {
		t.Errorf("expected empty string for empty input, got %q", decrypted)
	}
}

func TestEncrypt_OutputDiffersFromInput(t *testing.T) {
	original := "sk-my-secret-api-key"
	encrypted, err := Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}
	if encrypted == original {
		t.Error("encrypted output should differ from plaintext input")
	}
	if strings.HasPrefix(encrypted, "sk-") {
		t.Error("encrypted output should not start with 'sk-'")
	}
}

func TestEncrypt_DifferentCiphertextEachTime(t *testing.T) {
	// AES-GCM uses a random nonce, so same plaintext → different ciphertext
	original := "sk-abc123"
	enc1, _ := Encrypt(original)
	enc2, _ := Encrypt(original)

	if enc1 == enc2 {
		t.Error("two encryptions of same plaintext should produce different ciphertext (random nonce)")
	}

	// But both should decrypt to the same thing
	dec1, _ := Decrypt(enc1)
	dec2, _ := Decrypt(enc2)
	if dec1 != original || dec2 != original {
		t.Errorf("decryption mismatch: dec1=%q, dec2=%q, want %q", dec1, dec2, original)
	}
}

func TestDecrypt_GarbageInput(t *testing.T) {
	_, err := Decrypt("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for garbage input, got nil")
	}
}

func TestDecrypt_ValidBase64ButNotEncrypted(t *testing.T) {
	// Valid base64 but not a valid AES-GCM ciphertext
	_, err := Decrypt("aGVsbG8gd29ybGQ=") // "hello world" in base64
	if err == nil {
		t.Error("expected error for non-encrypted base64 data, got nil")
	}
}

func TestEncryptDecrypt_LongString(t *testing.T) {
	original := strings.Repeat("A", 10000)
	encrypted, err := Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}

	if decrypted != original {
		t.Errorf("roundtrip failed for long string (len %d)", len(original))
	}
}

func TestEncryptDecrypt_SpecialCharacters(t *testing.T) {
	original := "sk-ant-api03-key_with/special+chars=and!symbols@#$%"
	encrypted, err := Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt error: %v", err)
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt error: %v", err)
	}

	if decrypted != original {
		t.Errorf("roundtrip failed: got %q, want %q", decrypted, original)
	}
}
