package ilink

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptAESECB_RoundTrip(t *testing.T) {
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("hello world, this is a test message for AES-128-ECB encryption")

	ciphertext, err := EncryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(ciphertext)%16 != 0 {
		t.Errorf("ciphertext length %d not a multiple of 16", len(ciphertext))
	}

	decrypted, err := DecryptAESECB(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Errorf("decrypted mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptAESECB_WrongKeySize(t *testing.T) {
	_, err := EncryptAESECB([]byte("test"), []byte("short"))
	if err == nil {
		t.Error("expected error for short key")
	}
}

func TestDecryptAESECB_WrongKeySize(t *testing.T) {
	_, err := DecryptAESECB(make([]byte, 16), []byte("short"))
	if err == nil {
		t.Error("expected error for short key")
	}
}

func TestDecryptAESECB_BadBlockSize(t *testing.T) {
	key, _ := GenerateAESKey()
	_, err := DecryptAESECB([]byte("short"), key)
	if err == nil {
		t.Error("expected error for non-block-size ciphertext")
	}
}

func TestDecodeAESKey_Hex(t *testing.T) {
	hexKey := "0123456789abcdef0123456789abcdef"
	decoded, err := DecodeAESKey(hexKey)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	if len(decoded) != 16 {
		t.Errorf("len=%d, want 16", len(decoded))
	}
}

func TestDecodeAESKey_Base64Raw(t *testing.T) {
	// base64 of raw 16 bytes
	raw := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	encoded := EncodeAESKeyBase64(raw) // base64(hex) - the format used in CDNMedia
	// But DecodeAESKey should handle base64(hex) too since hex is 32 bytes and base64 of 32 bytes = ~44 bytes
	decoded, err := DecodeAESKey(encoded)
	if err != nil {
		t.Fatalf("decode base64(hex): %v", err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Errorf("decoded mismatch")
	}
}

func TestEncodeAESKeyHex(t *testing.T) {
	key := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	hexStr := EncodeAESKeyHex(key)
	if len(hexStr) != 32 {
		t.Errorf("hex len=%d, want 32", len(hexStr))
	}
}

func TestPkcs7PadUnpad(t *testing.T) {
	data := []byte("hello")
	padded := pkcs7Pad(data, 16)
	if len(padded) != 16 {
		t.Errorf("padded len=%d, want 16", len(padded))
	}
	unpadded, err := pkcs7Unpad(padded)
	if err != nil {
		t.Fatalf("unpad: %v", err)
	}
	if !bytes.Equal(unpadded, data) {
		t.Errorf("unpadded mismatch")
	}
}

func TestPkcs7Unpad_Invalid(t *testing.T) {
	_, err := pkcs7Unpad([]byte{})
	if err == nil {
		t.Error("expected error for empty data")
	}
	_, err = pkcs7Unpad([]byte{0x01, 0x02, 0xFF})
	if err == nil {
		t.Error("expected error for invalid padding")
	}
}
