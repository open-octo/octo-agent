package ilink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadMedia_DecryptsPlaintext(t *testing.T) {
	plaintext := []byte("hello from wechat file")
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	ciphertext, err := EncryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/downloadfile" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write(ciphertext)
	}))
	defer ts.Close()

	c := NewClient()
	CDNBaseURL = ts.URL
	defer func() { CDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c" }()

	got, err := c.DownloadMedia(context.Background(), &CDNMedia{
		EncryptQueryParam: "p=1",
		AESKey:            EncodeAESKeyBase64(key),
	})
	if err != nil {
		t.Fatalf("DownloadMedia error: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestDownloadMedia_UsesFullURL(t *testing.T) {
	plaintext := []byte("full url payload")
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	ciphertext, err := EncryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(ciphertext)
	}))
	defer ts.Close()

	c := NewClient()
	got, err := c.DownloadMedia(context.Background(), &CDNMedia{
		FullURL: ts.URL,
		AESKey:  EncodeAESKeyBase64(key),
	})
	if err != nil {
		t.Fatalf("DownloadMedia error: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestDownloadMedia_MissingAESKey(t *testing.T) {
	c := NewClient()
	_, err := c.DownloadMedia(context.Background(), &CDNMedia{
		EncryptQueryParam: "p=1",
	})
	if err == nil {
		t.Fatal("expected error for missing aes_key")
	}
}

func TestDownloadMedia_InvalidAESKey(t *testing.T) {
	c := NewClient()
	_, err := c.DownloadMedia(context.Background(), &CDNMedia{
		EncryptQueryParam: "p=1",
		AESKey:            "not-a-key",
	})
	if err == nil {
		t.Fatal("expected error for invalid aes_key")
	}
}
