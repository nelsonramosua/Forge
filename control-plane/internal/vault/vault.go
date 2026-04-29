package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

type Vault struct {
	aead cipher.AEAD
}

func New(masterKey []byte) (*Vault, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Encrypt(plaintext string, aad string) (nonceB64 string, ciphertextB64 string, err error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", err
	}
	ciphertext := v.aead.Seal(nil, nonce, []byte(plaintext), []byte(aad))
	return base64.StdEncoding.EncodeToString(nonce), base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (v *Vault) Decrypt(nonceB64 string, ciphertextB64 string, aad string) (string, error) {
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", err
	}
	plaintext, err := v.aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
