package tv

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

func (c Controller) ServiceMenu(ctx context.Context, menu string) error {
	cfg, err := c.store.load()
	if err != nil {
		return err
	}
	keycode, err := c.store.secret(keycodeAccount)
	if err != nil {
		return err
	}
	err = c.command(ctx, "ssap://system.launcher/launch", map[string]any{
		"id":     "com.webos.app.factorywin",
		"params": map[string]any{"id": "executeFactory", "irKey": menu},
	})
	if err != nil {
		return err
	}
	if keycode == "" {
		return errors.New("service menu opened. Enter the PIN on the TV or save the IP control keycode in Settings")
	}
	return sendServicePIN(ctx, cfg.Host, keycode)
}

func sendServicePIN(ctx context.Context, host, keycode string) error {
	salt := []byte{99, 97, 184, 14, 155, 220, 166, 99, 141, 7, 32, 242, 204, 86, 143, 185}
	key, err := pbkdf2.Key(sha256.New, keycode, salt, 16384, aes.BlockSize)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, "9761"))
	if err != nil {
		return fmt.Errorf("IP control: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := pause(ctx, 1800*time.Millisecond); err != nil {
		return err
	}
	for i, digit := range "0413" {
		if i > 0 {
			if err := pause(ctx, 450*time.Millisecond); err != nil {
				return err
			}
		}
		if err := sendIPKey(conn, block, "number"+string(digit)); err != nil {
			return fmt.Errorf("IP control: %w", err)
		}
	}
	return nil
}

func sendIPKey(conn net.Conn, block cipher.Block, key string) error {
	message := []byte("KEY_ACTION " + key + "\r")
	if len(message)%aes.BlockSize == 0 {
		message = append(message, ' ')
	}
	padding := aes.BlockSize - len(message)%aes.BlockSize
	message = append(message, bytes.Repeat([]byte{byte(padding)}, padding)...)

	var iv [aes.BlockSize]byte
	rand.Read(iv[:])
	data := make([]byte, aes.BlockSize+len(message))
	block.Encrypt(data, iv[:])
	cipher.NewCBCEncrypter(block, iv[:]).CryptBlocks(data[aes.BlockSize:], message)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(data); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, iv[:]); err != nil {
		return err
	}
	block.Decrypt(iv[:], iv[:])
	mode := cipher.NewCBCDecrypter(block, iv[:])
	var result []byte
	for len(result) < 4096 {
		var chunk [aes.BlockSize]byte
		if _, err := io.ReadFull(conn, chunk[:]); err != nil {
			return err
		}
		mode.CryptBlocks(chunk[:], chunk[:])
		if before, _, found := bytes.Cut(chunk[:], []byte("\n")); found {
			result = append(result, before...)
			break
		}
		result = append(result, chunk[:]...)
		pad := int(chunk[len(chunk)-1])
		if pad > 0 && pad <= aes.BlockSize && bytes.Equal(chunk[len(chunk)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
			result = result[:len(result)-pad]
			break
		}
	}
	if string(bytes.TrimSpace(result)) != "OK" {
		return fmt.Errorf("unexpected response %q", bytes.TrimSpace(result))
	}
	return nil
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
